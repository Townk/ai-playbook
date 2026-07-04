package driver

import (
	"errors"
	"strings"
	"testing"
)

// TestResolveShellName exercises the exported helper that callers outside the
// package (e.g. internal/author) use to get the effective shell name.
func TestResolveShellName(t *testing.T) {
	// Explicit "sh": universally available; must return "sh".
	if got := ResolveShellName("sh"); got != "sh" {
		t.Errorf("ResolveShellName(%q) = %q, want %q", "sh", got, "sh")
	}

	// Unknown selector → resolveShell returns an error → fallback must be "sh".
	if got := ResolveShellName("fish"); got != "sh" {
		t.Errorf("ResolveShellName(%q) = %q, want %q (error-fallback)", "fish", got, "sh")
	}
}

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
			// Explicit "sh" selector — sh adapter registered in Task 5.
			name:    "explicit sh → sh adapter",
			sel:     "sh",
			getenv:  noShellEnv,
			look:    makeLook("sh"),
			wantBin: "sh",
			wantA:   "sh",
		},
		{
			// zsh/bash absent, $SHELL unset, but sh present on PATH → sh fallback.
			name:    "all absent except sh → sh fallback",
			sel:     "",
			getenv:  noShellEnv,
			look:    makeLook("sh"),
			wantBin: "sh",
			wantA:   "sh",
		},
		{
			// Truly nothing on PATH and $SHELL unset — not even sh.
			name:    "all absent including sh → errUnsupportedShell",
			sel:     "",
			getenv:  noShellEnv,
			look:    makeLook(), // nothing on PATH
			wantErr: errUnsupportedShell,
		},
		{
			// $SHELL=/bin/bash with BOTH bash and zsh on PATH: $SHELL wins, so bash
			// adapter is returned even though zsh is also available.  This is the key
			// regression guard for the new $SHELL-first resolution order.
			name: "$SHELL=/bin/bash + zsh also on PATH → bash adapter ($SHELL wins)",
			sel:  "",
			getenv: func(k string) string {
				if k == "SHELL" {
					return "/bin/bash"
				}
				return ""
			},
			look:    makeLook("/bin/bash", "zsh"),
			wantBin: "/bin/bash",
			wantA:   "bash",
		},
		{
			// $SHELL points at fish (unsupported) but zsh is on PATH: resolution falls
			// through to the zsh fallback, NOT sh.
			name: "$SHELL=/usr/bin/fish (unsupported) + zsh on PATH → zsh fallback",
			sel:  "",
			getenv: func(k string) string {
				if k == "SHELL" {
					return "/usr/bin/fish"
				}
				return ""
			},
			look:    makeLook("zsh"),
			wantBin: "zsh",
			wantA:   "zsh",
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

// makeLookHelper mirrors the in-test makeLook: an injected look matching only the
// supplied names.
func makeLookHelper(found ...string) func(string) (string, error) {
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

// TestResolveShell_ErrorBranches covers the wrapped-error and fallthrough paths the
// happy-path table doesn't: explicit selectors whose binary is absent, an unknown
// selector, $SHELL pointing at sh / an unknown shell, and the $SHELL-absolute-path
// lookup failing so resolution falls through to the sh fallback.
func TestResolveShell_ErrorBranches(t *testing.T) {
	noEnv := func(string) string { return "" }
	shellEnv := func(v string) func(string) string {
		return func(k string) string {
			if k == "SHELL" {
				return v
			}
			return ""
		}
	}

	tests := []struct {
		name        string
		sel         string
		getenv      func(string) string
		look        func(string) (string, error)
		wantBin     string
		wantA       string
		wantErrSub  string // non-empty → expect an error whose message contains this
		wantErrSent error  // non-empty → expect errors.Is(err, sentinel)
	}{
		{
			name:       "explicit zsh missing → wrapped error",
			sel:        "zsh",
			getenv:     noEnv,
			look:       makeLookHelper(), // nothing on PATH
			wantErrSub: "zsh requested but not found",
		},
		{
			name:       "explicit bash missing → wrapped error",
			sel:        "bash",
			getenv:     noEnv,
			look:       makeLookHelper(),
			wantErrSub: "bash requested but not found",
		},
		{
			name:       "explicit sh missing → wrapped error",
			sel:        "sh",
			getenv:     noEnv,
			look:       makeLookHelper(),
			wantErrSub: "sh requested but not found",
		},
		{
			name:       "unknown selector → error",
			sel:        "fish",
			getenv:     noEnv,
			look:       makeLookHelper("zsh", "sh"),
			wantErrSub: "unknown shell selector",
		},
		{
			// auto + zsh absent + $SHELL=/bin/sh present → sh adapter via $SHELL.
			name:    "auto + SHELL=/bin/sh → sh via SHELL",
			sel:     "",
			getenv:  shellEnv("/bin/sh"),
			look:    makeLookHelper("/bin/sh"),
			wantBin: "/bin/sh",
			wantA:   "sh",
		},
		{
			// auto + $SHELL points at an unrecognized shell (fish): the switch has no
			// case for it, so resolution falls through to the sh fallback on PATH.
			name:    "auto + SHELL=/usr/bin/fish → sh fallback",
			sel:     "",
			getenv:  shellEnv("/usr/bin/fish"),
			look:    makeLookHelper("sh"),
			wantBin: "sh",
			wantA:   "sh",
		},
		{
			// auto + $SHELL=/opt/zsh but that absolute path isn't resolvable (look
			// fails): the zsh case's inner-if is false, so we fall through to the sh
			// fallback. Exercises the "matched base but look failed" branch.
			name:    "auto + SHELL=/opt/zsh unresolvable → sh fallback",
			sel:     "",
			getenv:  shellEnv("/opt/zsh"),
			look:    makeLookHelper("sh"), // look("zsh") and look("/opt/zsh") fail
			wantBin: "sh",
			wantA:   "sh",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, a, err := resolveShell(tc.sel, tc.getenv, tc.look)
			if tc.wantErrSub != "" || tc.wantErrSent != nil {
				if err == nil {
					t.Fatalf("resolveShell(%q) = nil error, want error", tc.sel)
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				if tc.wantErrSent != nil && !errors.Is(err, tc.wantErrSent) {
					t.Errorf("error = %v, want errors.Is %v", err, tc.wantErrSent)
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
