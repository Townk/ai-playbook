package driver

import (
	"errors"
	"testing"
)

func TestResolveShell(t *testing.T) {
	// makeLook returns an injected look function: calls that match an entry in
	// found return (name, nil); all others return an error.
	makeLook := func(found ...string) func(string) (string, error) {
		s := make(map[string]struct{}, len(found))
		for _, f := range found {
			s[f] = struct{}{}
		}
		return func(name string) (string, error) {
			if _, ok := s[name]; ok {
				return name, nil
			}
			return "", errors.New("not found: " + name)
		}
	}

	noShellEnv := func(string) string { return "" }
	zshOnPath := makeLook("zsh")

	tests := []struct {
		name    string
		sel     string
		getenv  func(string) string
		look    func(string) (string, error)
		wantBin string
		wantA   string // adapter.name()
		wantErr error
	}{
		{
			// Explicit "zsh" selector with zsh on PATH.
			name:    "explicit zsh found on PATH",
			sel:     "zsh",
			getenv:  noShellEnv,
			look:    zshOnPath,
			wantBin: "zsh",
			wantA:   "zsh",
		},
		{
			// Empty selector (acts like "auto") — zsh present on PATH.
			name:    "empty sel + zsh on PATH → zsh",
			sel:     "",
			getenv:  noShellEnv,
			look:    zshOnPath,
			wantBin: "zsh",
			wantA:   "zsh",
		},
		{
			// "auto" selector — zsh present on PATH.
			name:    "auto sel + zsh on PATH → zsh",
			sel:     "auto",
			getenv:  noShellEnv,
			look:    zshOnPath,
			wantBin: "zsh",
			wantA:   "zsh",
		},
		{
			// zsh absent from PATH but $SHELL=/usr/bin/zsh (absolute path is present).
			// Covers systems where zsh lives at an absolute path not on $PATH as "zsh".
			name: "auto + zsh absent + SHELL=/usr/bin/zsh → zsh via SHELL",
			sel:  "",
			getenv: func(k string) string {
				if k == "SHELL" {
					return "/usr/bin/zsh"
				}
				return ""
			},
			// look("zsh") fails; look("/usr/bin/zsh") succeeds.
			look:    makeLook("/usr/bin/zsh"),
			wantBin: "/usr/bin/zsh",
			wantA:   "zsh",
		},
		{
			// zsh absent, $SHELL=/bin/bash — bash adapter registered in Task 4.
			name: "auto + zsh absent + SHELL=/bin/bash → bash",
			sel:  "",
			getenv: func(k string) string {
				if k == "SHELL" {
					return "/bin/bash"
				}
				return ""
			},
			look:    makeLook("/bin/bash"),
			wantBin: "/bin/bash",
			wantA:   "bash",
		},
		{
			// Explicit "bash" selector — adapter registered in Task 4.
			name:    "explicit bash → bash adapter",
			sel:     "bash",
			getenv:  noShellEnv,
			look:    makeLook("bash"),
			wantBin: "bash",
			wantA:   "bash",
		},
		{
			// Explicit "sh" selector — no adapter yet (Task 5).
			name:    "explicit sh → errUnsupportedShell",
			sel:     "sh",
			getenv:  noShellEnv,
			look:    zshOnPath,
			wantErr: errUnsupportedShell,
		},
		{
			// No shell anywhere: look finds nothing, $SHELL unset.
			// Policy: return errUnsupportedShell (sh fallback deferred to Task 5).
			name:    "all absent → errUnsupportedShell",
			sel:     "",
			getenv:  noShellEnv,
			look:    makeLook(), // nothing on PATH
			wantErr: errUnsupportedShell,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, a, err := resolveShell(tc.sel, tc.getenv, tc.look)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("resolveShell(%q) error = %v, want %v", tc.sel, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveShell(%q) unexpected error: %v", tc.sel, err)
			}
			if bin != tc.wantBin {
				t.Errorf("bin = %q, want %q", bin, tc.wantBin)
			}
			if a.name() != tc.wantA {
				t.Errorf("adapter.name() = %q, want %q", a.name(), tc.wantA)
			}
		})
	}
}
