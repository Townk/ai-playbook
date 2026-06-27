package driver

import (
	"errors"
	"fmt"
	"path/filepath"
)

// errUnsupportedShell is returned by resolveShell when the requested or
// $SHELL-derived shell has no adapter registered yet. Task 5 will register the
// POSIX sh adapter, at which point only truly unknown names reach this error.
var errUnsupportedShell = errors.New("driver: unsupported shell; only zsh and bash are supported (sh adapter pending)")

// resolveShell picks the shell binary path and its shellAdapter from a selector
// string. sel may be "", "auto", "zsh", "bash", or "sh"; "" behaves identically
// to "auto".
//
// Resolution order for "" / "auto":
//  1. zsh by name: if look("zsh") succeeds, use zshAdapter.
//  2. $SHELL fallback: take filepath.Base(getenv("SHELL")).
//     - "zsh": try look(getenv("SHELL")) (the absolute path), use zshAdapter if found.
//     - "bash": try look(getenv("SHELL")) (the absolute path), use bashAdapter if found.
//     - "sh": return errUnsupportedShell (adapter registered in Task 5).
//     - anything else / SHELL unset: return errUnsupportedShell.
//
// All-absent policy: errUnsupportedShell (the sh fallback is deferred to Task 5
// when the POSIX sh adapter is registered).
//
// look and getenv are injected for testability; production callers pass
// exec.LookPath and os.Getenv.
func resolveShell(sel string, getenv func(string) string, look func(string) (string, error)) (bin string, a shellAdapter, err error) {
	switch sel {
	case "zsh":
		b, lerr := look("zsh")
		if lerr != nil {
			return "", nil, fmt.Errorf("driver: zsh requested but not found on PATH: %w", lerr)
		}
		return b, zshAdapter{}, nil

	case "bash":
		b, lerr := look("bash")
		if lerr != nil {
			return "", nil, fmt.Errorf("driver: bash requested but not found on PATH: %w", lerr)
		}
		return b, bashAdapter{}, nil

	case "sh":
		// Adapter registered in Task 5.
		return "", nil, errUnsupportedShell

	case "", "auto":
		// 1. Prefer zsh by name (the common case: zsh on PATH).
		if b, lerr := look("zsh"); lerr == nil {
			return b, zshAdapter{}, nil
		}

		// 2. Fall back to the $SHELL environment variable.
		shellEnv := getenv("SHELL")
		if shellEnv != "" {
			switch filepath.Base(shellEnv) {
			case "zsh":
				// zsh at an absolute path (e.g. $SHELL=/usr/bin/zsh but not on $PATH).
				if b, lerr := look(shellEnv); lerr == nil {
					return b, zshAdapter{}, nil
				}
			case "bash":
				// bash at an absolute path (e.g. $SHELL=/bin/bash but not on $PATH as "bash").
				if b, lerr := look(shellEnv); lerr == nil {
					return b, bashAdapter{}, nil
				}
			case "sh":
				// Recognised but not yet supported (adapter registered in Task 5).
				return "", nil, errUnsupportedShell
			}
		}

		// 3. No supported shell found anywhere.
		return "", nil, errUnsupportedShell

	default:
		return "", nil, fmt.Errorf("driver: unknown shell selector %q", sel)
	}
}
