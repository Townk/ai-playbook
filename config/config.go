// Package config loads ai-playbook's user configuration and merges it over a
// baked-in DEFAULT profile, so the binary works with NO config file present.
//
// Config lives at $XDG_CONFIG_HOME/ai-playbook/config.toml (fallback
// ~/.config/ai-playbook/config.toml). Only keys the user sets override the
// defaults; everything else falls through to the baked-in profile.
//
// The mux is configured as COMMAND TEMPLATES (no per-mux Go code): each action
// is a template string the user can override. The binary token-splits a template,
// substitutes placeholders, and runs the resulting argv directly (no shell). See
// Substitute for the placeholder set and the argv-safety contract.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Mux holds the command templates for terminal-multiplexer actions. Each value
// is a template string (see Substitute for placeholders). Empty strings after
// merge mean the action is unconfigured.
type Mux struct {
	OpenFloatingPane string `toml:"open-floating-pane"`
	OpenInputFloat   string `toml:"open-input-float"`
	OpenDockedPane   string `toml:"open-docked-pane"`
	DumpScreen       string `toml:"dump-screen"`
	TypeIntoPane     string `toml:"type-into-pane"`
}

// Agent holds the user's harness SELECTION and a few value preferences. It does
// NOT carry the invocation command: the harness invocation flags and the stream
// parser are one matched contract owned in-tree (package author + agentstream),
// so the user only picks WHICH harness plus these prefs.
//
//   - Harness: which shipped harness to drive ("claude"; pi/cursor are additive
//     later). Each supported harness is a matched {owned invocation, stream
//     adapter} pair.
//   - Model: the model id to pass the harness (empty → harness default).
//   - Bin: optional override for the harness executable path (empty → the
//     harness name resolved on PATH).
//   - Thinking: reasoning effort for the owned claude invocation, mapped to a
//     MAX_THINKING_TOKENS budget (off | low | medium | high, or a bare integer).
//     Empty defaults to "medium" so the model's reasoning streams as live
//     activity. "off" disables thinking. See author.claudeThinkingTokens.
type Agent struct {
	Harness  string `toml:"harness"`
	Model    string `toml:"model"`
	Bin      string `toml:"bin"`
	Thinking string `toml:"thinking"`
}

// Config is the merged ai-playbook configuration.
type Config struct {
	Mux   Mux   `toml:"mux"`
	Agent Agent `toml:"agent"`
}

// Default returns a fresh copy of the baked-in default profile. The mux defaults
// are the zellij commands the binary used before it was config-driven, so a
// no-config run behaves identically to the hardcoded zellij path.
func Default() *Config {
	return &Config{
		Mux: Mux{
			OpenFloatingPane: "zellij action new-pane --floating --width {width} --height {height} --close-on-exit {cwdarg} {namearg} -- {cmd}",
			// The request/ask INPUT float: borderless + pinned, with the widget's own
			// border as the only frame, sized in ABSOLUTE columns/rows ({width}/{height}
			// are bare integers here, not percents) — mirroring ai-assist-summon's
			// `--borderless true --pinned true --name "" --width 57 --height <measured>`.
			// A bare empty {name} after substitution drops, matching the shell's --name "".
			OpenInputFloat: `zellij action new-pane --floating --close-on-exit --name "" --borderless true --pinned true --width {width} --height {height} {cwdarg} -- {cmd}`,
			OpenDockedPane: "zellij action new-pane --direction right --close-on-exit {cwdarg} {namearg} -- {cmd}",
			DumpScreen:     "zellij action dump-screen {panearg}",
			TypeIntoPane:   "zellij action write-chars {text}",
		},
		Agent: Agent{
			Harness: "claude",
			Model:   "",
			Bin:     "",
			// "medium" → MAX_THINKING_TOKENS=8000 in the owned claude invocation, so
			// reasoning blocks stream as live activity by default. "off" disables it.
			Thinking: "medium",
		},
	}
}

// configPath resolves the config file location: $XDG_CONFIG_HOME/ai-playbook/
// config.toml, falling back to ~/.config/ai-playbook/config.toml.
func configPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-playbook", "config.toml")
}

// Load returns the merged configuration: the baked-in Default profile with any
// values from the user's config.toml laid over it. A missing file is NOT an
// error (the defaults stand alone). A present-but-unreadable or malformed file
// IS an error so misconfiguration is loud rather than silently ignored.
func Load() (*Config, error) {
	cfg := Default()
	path := configPath()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return loadFrom(cfg, path, data)
}

// loadFrom merges TOML data (from path, used in errors) over base in place and
// returns it. Factored out so tests can drive the merge without touching the
// filesystem layout.
func loadFrom(base *Config, path string, data []byte) (*Config, error) {
	// Decode only into the user struct, then merge non-empty fields over base.
	// (toml.Unmarshal leaves unset fields zero; merging by hand means an absent
	// key keeps the default rather than blanking it.)
	var user Config
	if err := toml.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	mergeStr(&base.Mux.OpenFloatingPane, user.Mux.OpenFloatingPane)
	mergeStr(&base.Mux.OpenInputFloat, user.Mux.OpenInputFloat)
	mergeStr(&base.Mux.OpenDockedPane, user.Mux.OpenDockedPane)
	mergeStr(&base.Mux.DumpScreen, user.Mux.DumpScreen)
	mergeStr(&base.Mux.TypeIntoPane, user.Mux.TypeIntoPane)
	mergeStr(&base.Agent.Harness, user.Agent.Harness)
	mergeStr(&base.Agent.Model, user.Agent.Model)
	mergeStr(&base.Agent.Bin, user.Agent.Bin)
	mergeStr(&base.Agent.Thinking, user.Agent.Thinking)
	return base, nil
}

// mergeStr overwrites *dst with v only when v is non-empty.
func mergeStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// Subst is the set of placeholder values for a single template expansion. A
// zero-valued field means the placeholder expands to nothing (an empty token is
// dropped, never emitted as an empty argv element). Cmd is multi-valued (a full
// command + args vector); the rest are single values.
type Subst struct {
	Cmd    []string // {cmd} — command + args (expands to multiple argv elements)
	Cwd    string   // {cwd}
	Pane   string   // {pane} / {panearg}
	Width  string   // {width}
	Height string   // {height}
	Title  string   // {title}
	Name   string   // {name} / {namearg}
	Text   string   // {text}
}

// Substitute token-splits template and substitutes placeholders, returning a
// ready-to-exec argv. It is deliberately NOT a shell: the template is split on
// whitespace into tokens, then each token is resolved. This keeps user-supplied
// values (paths with spaces, arbitrary {text}) safe — they become single argv
// elements and are never re-split or re-interpreted.
//
// Placeholders:
//
//   - Whole-token, multi-valued (the token IS exactly the placeholder):
//     {cmd}      → s.Cmd...            (each element a separate argv entry)
//     {namearg}  → "--name" s.Name     (or nothing when Name is empty)
//     {cwdarg}   → "--cwd" s.Cwd       (or nothing when Cwd is empty)
//     {panearg}  → "-p" s.Pane         (or nothing when Pane is empty)
//   - Anywhere in a token, single-valued (string substitution within the token):
//     {cwd} {pane} {width} {height} {title} {name} {text}
//     A token that, after substitution, is empty is dropped (so e.g. a bare
//     "{name}" with no name does not leave a stray empty argv element).
//
// If a template needs real shell features (pipes, expansion), the operator must
// wrap it explicitly, e.g. `sh -c "..."` as the template — Substitute will hand
// the whole thing to sh as argv elements. The default profile needs no shell.
func (m Mux) Substitute(template string, s Subst) []string {
	fields := strings.Fields(template)
	out := make([]string, 0, len(fields)+len(s.Cmd))
	for _, tok := range fields {
		switch tok {
		case "{cmd}":
			out = append(out, s.Cmd...)
			continue
		case "{namearg}":
			if s.Name != "" {
				out = append(out, "--name", s.Name)
			}
			continue
		case "{cwdarg}":
			if s.Cwd != "" {
				out = append(out, "--cwd", s.Cwd)
			}
			continue
		case "{panearg}":
			if s.Pane != "" {
				out = append(out, "-p", s.Pane)
			}
			continue
		case `""`, "''":
			// A literal empty-string token: emit an actual empty argv element rather
			// than dropping it (so a template's `--name ""` reaches the mux as an
			// empty title, matching ai-assist-summon's borderless `--name ""`). This
			// is NOT a shell, so the quotes are the operator's explicit "empty arg".
			out = append(out, "")
			continue
		}
		// Single-valued placeholders: substring substitution within the token.
		rep := strings.NewReplacer(
			"{cwd}", s.Cwd,
			"{pane}", s.Pane,
			"{width}", s.Width,
			"{height}", s.Height,
			"{title}", s.Title,
			"{name}", s.Name,
			"{text}", s.Text,
		)
		resolved := rep.Replace(tok)
		// Drop a token that resolved to empty (e.g. a lone "{name}" with no name);
		// but never drop a token that had no placeholder and was already non-empty.
		if resolved == "" && tok != resolved {
			continue
		}
		out = append(out, resolved)
	}
	return out
}
