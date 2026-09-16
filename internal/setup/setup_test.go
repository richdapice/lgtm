package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/config"
)

func TestDetectMonorepo(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest run","lint":"eslint . --ext .ts","typecheck":"tsc --noEmit"}}`), 0o644)
	os.MkdirAll(filepath.Join(root, "website"), 0o755)
	os.WriteFile(filepath.Join(root, "website", "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0o644)
	os.MkdirAll(filepath.Join(root, "server"), 0o755)
	os.WriteFile(filepath.Join(root, "server", "go.mod"), []byte("module x\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "dep"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "dep", "package.json"), []byte(`{}`), 0o644)

	d := Detect(root)
	if len(d.Projects) != 3 {
		t.Fatalf("projects = %+v", d.Projects)
	}
	if d.Projects[0].Path != "server" || d.Projects[0].Test != "go test ./..." {
		t.Fatalf("server = %+v", d.Projects[0])
	}
	if d.Projects[1].Path != "website" || d.Projects[1].Test != "npx jest --findRelatedTests {files}" || d.Projects[1].Lint != "" {
		t.Fatalf("website = %+v", d.Projects[1])
	}
	r := d.Projects[2]
	if r.Path != "." || r.Test != "npx vitest related {files} --run" || r.Lint != "npm run typecheck && npx eslint {files}" {
		t.Fatalf("root = %+v", r)
	}
}

func TestPromptDetectedIsOneQuestion(t *testing.T) {
	root := t.TempDir()
	d := Detected{Projects: []config.Project{{Path: ".", Test: "go test ./...", Lint: "go vet ./..."}}}
	var out strings.Builder
	r := Prompt(strings.NewReader("\n"), &out, d, false) // Enter = keep
	if r.Projects[0].Test != "go test ./..." || r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 3 {
		t.Fatalf("kept = %+v", r)
	}
	if strings.Contains(out.String(), "mode (") {
		t.Fatal("asked about mode; defaults should not be questions")
	}
	if err := Write(root, r); err != nil {
		t.Fatal(err)
	}
	back, _ := config.LoadRepo(root)
	if back.Projects[0].Test != "go test ./..." {
		t.Fatalf("round trip = %+v", back)
	}
}

func TestPromptBlankProjectValidatesCommands(t *testing.T) {
	d := Detected{Projects: []config.Project{{Path: "."}}, Hints: []string{"Xcode project. Likely commands:"}}
	var out strings.Builder
	// lint: "iij" (not on PATH) -> use anyway? n -> then "true" (on PATH); test: empty; suite: empty
	r := Prompt(strings.NewReader("iij\nn\ntrue\n\n\n"), &out, d, false)
	if r.Projects[0].Lint != "true" || r.Projects[0].Test != "" {
		t.Fatalf("got %+v", r.Projects[0])
	}
	if !strings.Contains(out.String(), "Xcode project") || !strings.Contains(out.String(), `"iij" isn't on your PATH`) {
		t.Fatalf("prompt output:\n%s", out.String())
	}
}

func TestHintsRecognizeXcode(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "App.xcodeproj"), 0o755)
	d := Detect(root)
	if len(d.Hints) == 0 || !strings.Contains(d.Hints[0], "Xcode") {
		t.Fatalf("hints = %v", d.Hints)
	}
}

func TestPromptYesSkipsQuestions(t *testing.T) {
	r := Prompt(strings.NewReader(""), &strings.Builder{}, Detected{Projects: []config.Project{{Path: "."}}}, true)
	if r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 3 {
		t.Fatalf("defaults = %+v", r.Settings)
	}
}
