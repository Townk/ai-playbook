package driver

import (
	"errors"
	"fmt"
	"path/filepath"
)

// errUnsupportedShell is returned by resolveShell when no adapter-backed shell
// (zsh, bash, or POSIX sh) can be found anywhere — not on PATH by name, not via
// $SHELL, and not even sh as the final fallback.
var errUnsupportedShell = errors.New("driver: no supported shell found (zsh, bash, or sh)")

// resolveShell picks the shell binary path and its shellAdapter from a selector
// string. sel may be "", "auto", "zsh", "bash", or "sh"; "" behaves identically
// to "auto".
//
// Resolution order for "" / "auto":
//  1. zsh by name: if look("zsh") succeeds, use zshAdapter.
//  2. $SHELL fallback: take filepath.Base(getenv("SHELL")).
//     - "zsh": try look(getenv("SHELL")) (the absolute path), use zshAdapter if found.
//     - "bash": try look(getenv("SHELL")) (the absolute path), use bashAdapter if found.
//     - "sh": try look(getenv("SHELL")) (the absolute path), use shAdapter if found.
//  3. Final fallback: look("sh") → shAdapter (covers $SHELL unset or pointing at
//     an unknown shell, as long as a POSIX sh exists on PATH).
//
// All-absent policy: errUnsupportedShell only when not even sh can be found.
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
		b, lerr := look("sh")
		if lerr != nil {
			return "", nil, fmt.Errorf("driver: sh requested but not found on PATH: %w", lerr)
		}
		return b, shAdapter{}, nil

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
				// sh at an absolute path (e.g. $SHELL=/bin/sh or dash).
				if b, lerr := look(shellEnv); lerr == nil {
					return b, shAdapter{}, nil
				}
			}
		}

		// 3. Final fallback: any POSIX sh on PATH. sh is the lowest-common
		//    denominator, so it is the last resort before giving up.
		if b, lerr := look("sh"); lerr == nil {
			return b, shAdapter{}, nil
		}

		// 4. No supported shell found anywhere — not even sh.
		return "", nil, errUnsupportedShell

	default:
		return "", nil, fmt.Errorf("driver: unknown shell selector %q", sel)
	}
}
