package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if _, err := Run(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	Run(ctx, dir, "add", ".")
	Run(ctx, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestBranchDiffAndTree(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	Run(ctx, dir, "checkout", "-q", "-b", "feat/x")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)
	Run(ctx, dir, "commit", "-qam", "add two")

	if b, _ := Branch(ctx, dir); b != "feat/x" {
		t.Fatalf("branch = %q", b)
	}
	mb, err := MergeBase(ctx, dir, "main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := Diff(ctx, dir, mb, "HEAD")
	if d == "" || !contains(d, "+two") {
		t.Fatalf("diff = %q", d)
	}
	files, _ := ChangedFiles(ctx, dir, mb, "HEAD")
	if len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("files = %v", files)
	}
	if ex, _ := ChangedFiles(ctx, dir, mb, "HEAD", ":!a.txt"); len(ex) != 0 {
		t.Fatalf("pathspec exclusion ignored: %v", ex)
	}
	t1, _ := TreeHash(ctx, dir, "HEAD")
	Run(ctx, dir, "commit", "-q", "--allow-empty", "--amend", "-m", "reworded")
	t2, _ := TreeHash(ctx, dir, "HEAD")
	if t1 != t2 {
		t.Fatal("tree hash changed on a metadata-only amend")
	}
	clean, _ := IsClean(ctx, dir)
	if !clean {
		t.Fatal("fresh commit should be clean")
	}
	os.WriteFile(filepath.Join(dir, ".lgtm.toml"), []byte("x"), 0o644)
	if c, _ := IsClean(ctx, dir); c {
		t.Fatal("untracked file should dirty the tree")
	}
	if c, _ := IsClean(ctx, dir, ":!.lgtm.toml"); !c {
		t.Fatal("ignored pathspec should not dirty the tree")
	}
	cd, err := CommonDir(ctx, dir)
	if err != nil || !filepath.IsAbs(cd) || filepath.Base(cd) != ".git" {
		t.Fatalf("common dir = %q (%v)", cd, err)
	}
}

func TestDetachedHeadIsAnError(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	Run(ctx, dir, "checkout", "-q", "--detach")
	if _, err := Branch(ctx, dir); err == nil {
		t.Fatal("detached HEAD accepted as a branch")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestFindCommonDirAndBranchFastInWorktree(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := Run(ctx, dir, "worktree", "add", "-q", "-b", "wt-branch", wt); err != nil {
		t.Fatal(err)
	}
	want, _ := CommonDir(ctx, wt)
	got, err := FindCommonDir(filepath.Join(wt))
	if err != nil || got != want {
		t.Fatalf("FindCommonDir = %q (%v), want %q", got, err, want)
	}
	if b := BranchFast(wt); b != "wt-branch" {
		t.Fatalf("BranchFast = %q", b)
	}
	if b := BranchFast(dir); b != "main" {
		t.Fatalf("BranchFast main = %q", b)
	}
}
