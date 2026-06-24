package kb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRoot_DataDirOverride(t *testing.T) {
	t.Setenv("AI_ASSIST_DATA_DIR", "/explicit/data")
	if got := DefaultRoot(); got != "/explicit/data" {
		t.Fatalf("DefaultRoot = %q, want /explicit/data", got)
	}
}

func TestDefaultRoot_XDG(t *testing.T) {
	t.Setenv("AI_ASSIST_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := DefaultRoot(); got != filepath.Join("/xdg", "ai-assist") {
		t.Fatalf("DefaultRoot = %q, want /xdg/ai-assist", got)
	}
}

// Path matches the shell layout: $root/projects/<sha1(projectRoot)>/knowledge.md.
// The key is the SHA-1 of the literal path string (verified against the known
// shasum of "/p").
func TestPath_ShellLayout(t *testing.T) {
	// printf '%s' /p | shasum -a 1  →  the value below
	const wantKey = "ca85a389d362533706fa2f54ec9af609a5b8a397"
	got := Path("/root", "/p")
	want := filepath.Join("/root", "projects", wantKey, "knowledge.md")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestLoadFrom_MissingIsEmpty(t *testing.T) {
	if kb := LoadFrom(t.TempDir(), "/some/project"); kb != "" {
		t.Fatalf("missing KB should be empty, got %q", kb)
	}
}

func TestLoadFrom_ReadsFile(t *testing.T) {
	root := t.TempDir()
	const project = "/Users/me/proj"
	const facts = "uses bazel, not make\nrun via ./x.sh\n"
	p := Path(root, project)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(facts), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadFrom(root, project); string(got) != facts {
		t.Fatalf("LoadFrom = %q, want %q", got, facts)
	}
}

func TestLoad_DefaultRootRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_ASSIST_DATA_DIR", root)
	const project = "/Users/me/widget"
	p := Path(root, project)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("fact one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(project); string(got) != "fact one" {
		t.Fatalf("Load = %q, want %q", got, "fact one")
	}
}
