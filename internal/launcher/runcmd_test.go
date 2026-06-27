package launcher

import (
	"os"
	"testing"
)

// ---- resolveRunArgs — the run argument resolution matrix ----

func TestResolveRunArgs_Matrix(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantKind  string
		wantValue string
		wantErr   bool
	}{
		{"bare positional ⇒ playbook", []string{"build"}, "playbook", "build", false},
		{"--playbook flag", []string{"--playbook", "build"}, "playbook", "build", false},
		{"--file flag", []string{"--file", "/p.md"}, "file", "/p.md", false},
		{"both --playbook and --file → error", []string{"--playbook", "build", "--file", "/p.md"}, "", "", true},
		{"positional + --file → error", []string{"build", "--file", "/p.md"}, "", "", true},
		{"none → error", []string{}, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, value, err := resolveRunArgs(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveRunArgs(%v): want error, got (%q,%q,nil)", c.args, kind, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRunArgs(%v): unexpected error: %v", c.args, err)
			}
			if kind != c.wantKind {
				t.Errorf("kind = %q, want %q", kind, c.wantKind)
			}
			if value != c.wantValue {
				t.Errorf("value = %q, want %q", value, c.wantValue)
			}
		})
	}
}

// ---- RunMain routing via the uiMainFn + pathForFn seams ----

// TestRunMain_BareSlug_ReshapesToFile verifies a bare slug is resolved via PathFor
// and the reshaped os.Args hand ui.Main `run --file <resolved-path>`.
func TestRunMain_BareSlug_ReshapesToFile(t *testing.T) {
	const wantPath = "/store/build-app.md"
	withArgs(t, []string{"ai-playbook", "run", "build-app"})
	withPathForFn(t, func(slug string) (string, bool) {
		if slug == "build-app" {
			return wantPath, true
		}
		return "", false
	})

	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain bare slug: want exit 0, got %d", code)
	}
	if len(captured) != 4 {
		t.Fatalf("RunMain: os.Args not reshaped to {bin,run,--file,path}; got %v", captured)
	}
	if captured[1] != "run" || captured[2] != "--file" || captured[3] != wantPath {
		t.Errorf("RunMain: reshaped args = %v, want [bin run --file %s]", captured, wantPath)
	}
}

// TestRunMain_File_ReshapesToFile verifies --file is passed straight through (no
// PathFor resolution) as `run --file <path>`.
func TestRunMain_File_ReshapesToFile(t *testing.T) {
	const wantPath = "/tmp/raw.md"
	withArgs(t, []string{"ai-playbook", "run", "--file", wantPath})

	var captured []string
	withUIMainFn(t, func() int {
		captured = append([]string{}, os.Args...)
		return 0
	})

	if code := RunMain(); code != 0 {
		t.Fatalf("RunMain --file: want exit 0, got %d", code)
	}
	if len(captured) != 4 || captured[2] != "--file" || captured[3] != wantPath {
		t.Errorf("RunMain --file: reshaped args = %v, want [bin run --file %s]", captured, wantPath)
	}
}

// TestRunMain_UnknownSlug_Exit1 verifies an unknown slug is a clear error (exit 1)
// and ui.Main is never reached.
func TestRunMain_UnknownSlug_Exit1(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "run", "no-such-slug"})
	withPathForFn(t, func(string) (string, bool) { return "", false })

	called := false
	withUIMainFn(t, func() int { called = true; return 0 })

	if code := RunMain(); code != 1 {
		t.Errorf("RunMain unknown slug: want exit 1, got %d", code)
	}
	if called {
		t.Error("RunMain unknown slug: ui.Main must not be called")
	}
}

// TestRunMain_NoArgs_Exit2 verifies missing a source is a usage error (exit 2).
func TestRunMain_NoArgs_Exit2(t *testing.T) {
	withArgs(t, []string{"ai-playbook", "run"})

	called := false
	withUIMainFn(t, func() int { called = true; return 0 })

	if code := RunMain(); code != 2 {
		t.Errorf("RunMain no args: want exit 2, got %d", code)
	}
	if called {
		t.Error("RunMain no args: ui.Main must not be called")
	}
}
