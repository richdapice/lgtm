// Package hook is lgtm on every push. A git pre-push hook calls `lgtm hook
// pre-push`; it reviews and fixes the branch before anything leaves the
// machine. Git has already decided what to push by the time the hook runs, so
// if lgtm commits fixes the push is stopped with "push again" — and the second
// push is instant, because that tree is on the reviewed list.
package hook

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/gitx"
	"github.com/richdapice/lgtm/internal/run"
)

const script = `#!/bin/sh
# installed by lgtm init --hook — review and fix before anything leaves
exec lgtm hook pre-push "$@"
`

// Install writes the pre-push hook into the repo's shared hooks dir, so every
// worktree gets it. It refuses to overwrite a hook that isn't ours.
func Install(ctx context.Context, dir string) (path string, err error) {
	common, err := gitx.CommonDir(ctx, dir)
	if err != nil {
		return "", err
	}
	path = filepath.Join(common, "hooks", "pre-push")
	if cur, err := os.ReadFile(path); err == nil && !strings.Contains(string(cur), "lgtm hook") {
		return path, fmt.Errorf("%s exists and isn't lgtm's; move it aside first", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(script), 0o755)
}

// PrePush is the hook body. stdin carries "<local ref> <local sha> <remote
// ref> <remote sha>" per ref being pushed. Only a push of the current branch
// is reviewed; deletes, tags, and other branches pass through.
func PrePush(ctx context.Context, dir string, stdin io.Reader, out io.Writer, logger *log.Logger) (exit int) {
	root, err := gitx.Root(ctx, dir)
	if err != nil {
		return 0 // not a repo we understand; let git decide
	}
	common, _ := gitx.CommonDir(ctx, root)
	branch, err := gitx.Branch(ctx, root)
	if err != nil {
		return 0
	}
	head, _ := gitx.Run(ctx, root, "rev-parse", "HEAD")
	pushingHead := false
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 4 {
			continue
		}
		if strings.HasPrefix(f[0], "refs/heads/") && f[1] == head {
			pushingHead = true
		}
	}
	if !pushingHead {
		return 0
	}
	tree, _ := gitx.TreeHash(ctx, root, "HEAD")
	if run.Reviewed(common, tree) {
		fmt.Fprintf(out, "lgtm: %s already reviewed\n", branch)
		return 0
	}
	fmt.Fprintf(out, "lgtm: reviewing %s before it leaves\n", branch)
	err = ceremony.Run(ctx, ceremony.Options{Dir: root, NoPR: true, Out: out, In: os.Stdin, Log: logger})
	after, _ := gitx.Run(ctx, root, "rev-parse", "HEAD")
	switch {
	case errors.Is(err, ceremony.ErrOnBase):
		return 0
	case errors.Is(err, ceremony.ErrHeld):
		fmt.Fprintln(out, "lgtm: findings need you — push held. `lgtm` to decide, or git push --no-verify.")
		return 2
	case err != nil:
		fmt.Fprintln(out, "lgtm:", err)
		return 1
	case after != head:
		fmt.Fprintf(out, "lgtm: fixes committed on %s — push again to send them (this tree is now reviewed).\n", branch)
		return 1
	}
	return 0
}
