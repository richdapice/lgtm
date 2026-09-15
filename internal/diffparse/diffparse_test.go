package diffparse

import (
	"os"
	"testing"
)

func TestParseRealDiff(t *testing.T) {
	raw, err := os.ReadFile("testdata/real_pr.diff")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path() != "scripts/seed-demo.ts" {
		t.Errorf("path = %q", f.Path())
	}
	if len(f.Hunks) != 9 {
		t.Errorf("want 9 hunks, got %d", len(f.Hunks))
	}

	// First hunk header is @@ -200,6 +200,11 @@ — verify numbering starts there.
	first := f.Hunks[0].Lines[0]
	if first.Kind != Context || first.OldNum != 200 || first.NewNum != 200 {
		t.Errorf("first line = %+v", first)
	}

	// Line numbers must be monotonically consistent within every hunk.
	for _, h := range f.Hunks {
		var lastOld, lastNew int
		for _, l := range h.Lines {
			if l.OldNum != 0 {
				if l.OldNum <= lastOld {
					t.Fatalf("old numbering not increasing in %q: %d after %d", h.Header, l.OldNum, lastOld)
				}
				lastOld = l.OldNum
			}
			if l.NewNum != 0 {
				if l.NewNum <= lastNew {
					t.Fatalf("new numbering not increasing in %q: %d after %d", h.Header, l.NewNum, lastNew)
				}
				lastNew = l.NewNum
			}
		}
	}
}

const syntheticDiff = `diff --git a/old.txt b/old.txt
deleted file mode 100644
index e69de29..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-gone
-also gone
diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3b18e51
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello world
diff --git a/a.png b/a.png
index 1111111..2222222 100644
Binary files a/a.png and b/a.png differ
diff --git a/before.go b/after.go
similarity index 90%
rename from before.go
rename to after.go
index 3333333..4444444 100644
--- a/before.go
+++ b/after.go
@@ -10,2 +10,2 @@ func main() {
 	x := 1
-	y := 2
+	y := 3
`

func TestParseSynthetic(t *testing.T) {
	files, err := Parse(syntheticDiff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("want 4 files, got %d", len(files))
	}

	del := files[0]
	if !del.IsDeleted || del.Path() != "old.txt" {
		t.Errorf("deleted file: %+v", del)
	}
	if l := del.Hunks[0].Lines[0]; l.Kind != Removed || l.OldNum != 1 {
		t.Errorf("deleted line = %+v", l)
	}

	added := files[1]
	if !added.IsNew || added.Path() != "new.txt" {
		t.Errorf("new file: %+v", added)
	}
	if l := added.Hunks[0].Lines[0]; l.Kind != Added || l.NewNum != 1 || l.Text != "hello world" {
		t.Errorf("added line = %+v", l)
	}

	if !files[2].IsBinary {
		t.Errorf("binary not detected: %+v", files[2])
	}

	ren := files[3]
	if !ren.IsRename || ren.OldPath != "before.go" || ren.Path() != "after.go" {
		t.Errorf("rename: %+v", ren)
	}
	if l := ren.Hunks[0].Lines[2]; l.Kind != Added || l.NewNum != 11 || l.Text != "\ty := 3" {
		t.Errorf("rename hunk line = %+v", l)
	}
}

func TestCommentable(t *testing.T) {
	files, err := Parse(syntheticDiff)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		line int
		side string
		want bool
	}{
		{"new.txt", 1, "RIGHT", true},
		{"new.txt", 2, "RIGHT", false},
		{"old.txt", 1, "LEFT", true},
		{"old.txt", 1, "RIGHT", false},
		{"after.go", 11, "RIGHT", true}, // the added y:=3
		{"after.go", 11, "LEFT", true},  // the removed y:=2
		{"after.go", 12, "LEFT", false},
		{"nope.txt", 1, "RIGHT", false},
	}
	for _, c := range cases {
		if got := Commentable(files, c.path, c.line, c.side); got != c.want {
			t.Errorf("Commentable(%s,%d,%s) = %v, want %v", c.path, c.line, c.side, got, c.want)
		}
	}

	if got := NearestCommentable(files, "after.go", 13, 5); got != 11 {
		t.Errorf("NearestCommentable = %d, want 11", got)
	}
	if got := NearestCommentable(files, "after.go", 50, 5); got != 0 {
		t.Errorf("NearestCommentable far = %d, want 0", got)
	}
}
