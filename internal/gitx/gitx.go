// Package gitx is the handful of git commands the ceremony needs. It shells out
// rather than linking a git library for the same reason gh is shelled: it
// behaves exactly like the git the user already trusts, hooks and config
// included.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Root is the worktree's top level.
func Root(ctx context.Context, dir string) (string, error) {
	return Run(ctx, dir, "rev-parse", "--show-toplevel")
}

// CommonDir is the .git directory shared by every worktree of the repo,
// absolute. Run state lives under it so all worktrees see all runs.
func CommonDir(ctx context.Context, dir string) (string, error) {
	p, err := Run(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		root, err := Root(ctx, dir)
		if err != nil {
			return "", err
		}
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), nil
}

func Branch(ctx context.Context, dir string) (string, error) {
	b, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if b == "HEAD" {
		return "", fmt.Errorf("git: detached HEAD; check out a branch first")
	}
	return b, nil
}

func MergeBase(ctx context.Context, dir, base, head string) (string, error) {
	return Run(ctx, dir, "merge-base", base, head)
}

// Diff is the unified diff from the merge-base to head — the PR's diff, not
// "everything that differs from main", which would include main's own progress.
func Diff(ctx context.Context, dir, mergeBase, head string) (string, error) {
	return Run(ctx, dir, "diff", "--no-color", "--no-ext-diff", mergeBase, head)
}

func ChangedFiles(ctx context.Context, dir, mergeBase, head string) ([]string, error) {
	out, err := Run(ctx, dir, "diff", "--name-only", mergeBase, head)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// TreeHash identifies content independent of commit metadata. Two commits with
// the same tree have nothing new to review.
func TreeHash(ctx context.Context, dir, ref string) (string, error) {
	return Run(ctx, dir, "rev-parse", ref+"^{tree}")
}

func IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// RefExists reports whether a ref resolves — used to check the base branch and
// whether origin/<base> is fetched.
func RefExists(ctx context.Context, dir, ref string) bool {
	_, err := Run(ctx, dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

func Push(ctx context.Context, dir, remote, branch string) error {
	_, err := Run(ctx, dir, "push", "-u", remote, branch)
	return err
}

// ShowFile returns a file's contents at a ref, for giving a lens context beyond
// the hunk.
func ShowFile(ctx context.Context, dir, ref, path string) (string, error) {
	return Run(ctx, dir, "show", ref+":"+path)
}

// FindCommonDir resolves the git common dir without spawning git, for the
// statusline, which runs on every refresh tick. It walks up from dir to a .git
// entry: a directory is the common dir; a file (a worktree) names the gitdir,
// whose "commondir" file points at the shared one.
func FindCommonDir(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		g := filepath.Join(dir, ".git")
		st, err := os.Stat(g)
		if err == nil {
			if st.IsDir() {
				return g, nil
			}
			b, err := os.ReadFile(g)
			if err != nil {
				return "", err
			}
			gitdir := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(dir, gitdir)
			}
			c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
			if err != nil {
				return filepath.Clean(gitdir), nil
			}
			common := strings.TrimSpace(string(c))
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitdir, common)
			}
			return filepath.Clean(common), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("git: not inside a repository: %s", dir)
		}
		dir = parent
	}
}

// BranchFast reads HEAD without spawning git, same reason as FindCommonDir.
func BranchFast(dir string) string {
	dir, _ = filepath.Abs(dir)
	for {
		g := filepath.Join(dir, ".git")
		st, err := os.Stat(g)
		if err == nil {
			head := filepath.Join(g, "HEAD")
			if !st.IsDir() {
				b, _ := os.ReadFile(g)
				gd := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(dir, gd)
				}
				head = filepath.Join(gd, "HEAD")
			}
			b, err := os.ReadFile(head)
			if err != nil {
				return ""
			}
			return strings.TrimPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
