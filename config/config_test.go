package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No config file → the baked-in default profile stands alone (zellij defaults).
func TestLoad_NoFile_Defaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty dir → no config.toml
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if cfg.Mux != def.Mux {
		t.Fatalf("mux defaults differ:\n got %+v\nwant %+v", cfg.Mux, def.Mux)
	}
	if !strings.HasPrefix(cfg.Mux.DumpScreen, "zellij action dump-screen") {
		t.Fatalf("dump-screen default = %q", cfg.Mux.DumpScreen)
	}
	// The agent defaults: harness "claude", everything else empty.
	if cfg.Agent != def.Agent {
		t.Fatalf("agent defaults differ:\n got %+v\nwant %+v", cfg.Agent, def.Agent)
	}
	if cfg.Agent.Harness != "claude" {
		t.Fatalf("agent.harness default = %q, want claude", cfg.Agent.Harness)
	}
	if cfg.Agent.Model != "" || cfg.Agent.Bin != "" || cfg.Agent.Thinking != "" {
		t.Fatalf("agent non-harness defaults should be empty: %+v", cfg.Agent)
	}
}

// XDG path takes precedence; a present file's keys override the defaults.
func TestLoad_XDGPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "ai-playbook")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[mux]\ndump-screen = \"tmux capture-pane -p\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mux.DumpScreen != "tmux capture-pane -p" {
		t.Fatalf("override not applied: %q", cfg.Mux.DumpScreen)
	}
	// Untouched keys keep their defaults.
	if cfg.Mux.OpenFloatingPane != Default().Mux.OpenFloatingPane {
		t.Fatalf("untouched key was blanked: %q", cfg.Mux.OpenFloatingPane)
	}
}

// loadFrom merges only non-empty keys over the base default (absent → default).
func TestMerge_OnlyOverridesPresentKeys(t *testing.T) {
	data := []byte("[mux]\nopen-floating-pane = \"wezterm spawn -- {cmd}\"\n\n[agent]\nmodel = \"opus\"\nbin = \"/opt/claude\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Mux.OpenFloatingPane != "wezterm spawn -- {cmd}" {
		t.Fatalf("float override: %q", cfg.Mux.OpenFloatingPane)
	}
	if cfg.Mux.DumpScreen != Default().Mux.DumpScreen {
		t.Fatalf("dump-screen should keep default: %q", cfg.Mux.DumpScreen)
	}
	// Present [agent] keys override; absent keys keep their defaults.
	if cfg.Agent.Model != "opus" {
		t.Fatalf("agent.model override: %q", cfg.Agent.Model)
	}
	if cfg.Agent.Bin != "/opt/claude" {
		t.Fatalf("agent.bin override: %q", cfg.Agent.Bin)
	}
	if cfg.Agent.Harness != "claude" {
		t.Fatalf("agent.harness should keep default: %q", cfg.Agent.Harness)
	}
	if cfg.Agent.Thinking != "" {
		t.Fatalf("agent.thinking should keep empty default: %q", cfg.Agent.Thinking)
	}
}

// A user [agent].harness overrides the default selection.
func TestMerge_AgentHarnessOverride(t *testing.T) {
	data := []byte("[agent]\nharness = \"pi\"\nthinking = \"high\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Agent.Harness != "pi" {
		t.Fatalf("agent.harness override: %q", cfg.Agent.Harness)
	}
	if cfg.Agent.Thinking != "high" {
		t.Fatalf("agent.thinking override: %q", cfg.Agent.Thinking)
	}
}

// Malformed TOML is a loud error, not silently ignored.
func TestLoadFrom_MalformedErrors(t *testing.T) {
	if _, err := loadFrom(Default(), "bad.toml", []byte("[mux\n")); err == nil {
		t.Fatal("malformed TOML should error")
	}
}

// Substitute: {cmd} expands to multiple argv elements; paired flags drop empty.
func TestSubstitute_CmdAndPairs(t *testing.T) {
	tpl := Default().Mux
	got := tpl.Substitute(tpl.OpenFloatingPane, Subst{
		Cmd:    []string{"delta", "/p"},
		Width:  "90%",
		Height: "90%",
	})
	// No cwd/name → --cwd and --name absent; {cmd} split into two elements.
	joined := strings.Join(got, "\x00")
	if strings.Contains(joined, "--cwd") || strings.Contains(joined, "--name") {
		t.Fatalf("empty paired flags should be dropped: %v", got)
	}
	if got[len(got)-2] != "delta" || got[len(got)-1] != "/p" {
		t.Fatalf("{cmd} not expanded to argv: %v", got)
	}
}

// Substitute keeps a path-with-spaces as a SINGLE argv element (no re-splitting).
func TestSubstitute_PathWithSpaces(t *testing.T) {
	tpl := Default().Mux
	got := tpl.Substitute(tpl.OpenFloatingPane, Subst{
		Cmd:    []string{"less", "/Users/me/My Projects/a b.txt"},
		Cwd:    "/Users/me/My Projects",
		Name:   "diff x",
		Width:  "90%",
		Height: "90%",
	})
	if !containsExact(got, "/Users/me/My Projects/a b.txt") {
		t.Fatalf("spaced cmd arg was split: %v", got)
	}
	if !containsExact(got, "/Users/me/My Projects") {
		t.Fatalf("spaced cwd was split: %v", got)
	}
	if !containsExact(got, "diff x") {
		t.Fatalf("spaced name was split: %v", got)
	}
}

// {text} (the play action) survives as one argv element even with spaces.
func TestSubstitute_TextWithSpaces(t *testing.T) {
	tpl := Default().Mux
	got := tpl.Substitute(tpl.TypeIntoPane, Subst{Text: "git commit -m 'hi there'"})
	if !containsExact(got, "git commit -m 'hi there'") {
		t.Fatalf("text was split: %v", got)
	}
}

func containsExact(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
