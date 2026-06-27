package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A malformed config.toml IS an error, but Load/loadFrom must still return a
// usable non-nil *Config (the defaults) so callers that ignore the error
// (cfg, _ := Load()) never deref nil. Regression guard for the nil-config panic.
func TestLoadFrom_ParseError_ReturnsUsableDefault(t *testing.T) {
	cfg, err := loadFrom(Default(), "bad.toml", []byte("this is = not [valid toml"))
	if err == nil {
		t.Fatal("expected a parse error for malformed toml")
	}
	if cfg == nil {
		t.Fatal("loadFrom returned nil *Config on parse error — callers would panic")
	}
	if got := cfg.Driver.Shell; got != "" {
		t.Fatalf("fallback cfg not the default profile: Driver.Shell = %q, want \"\" (auto)", got)
	}
	if cfg.GlobalStoreDir() == "" {
		t.Fatal("fallback cfg.GlobalStoreDir() empty — deref-safety not preserved")
	}
}

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
	if cfg.Agent.Model != "" || cfg.Agent.Bin != "" {
		t.Fatalf("agent model/bin defaults should be empty: %+v", cfg.Agent)
	}
	if cfg.Agent.Thinking != "medium" {
		t.Fatalf("agent.thinking default = %q, want medium", cfg.Agent.Thinking)
	}
	// The cheap classify pass defaults to the "haiku" model alias.
	if cfg.Agent.TriageModel != "haiku" {
		t.Fatalf("agent.triage_model default = %q, want haiku", cfg.Agent.TriageModel)
	}
}

// triage_model is parsed from a present [agent] block; an absent key keeps the
// "haiku" default.
func TestMerge_TriageModel(t *testing.T) {
	data := []byte("[agent]\ntriage_model = \"claude-3-5-haiku-latest\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Agent.TriageModel != "claude-3-5-haiku-latest" {
		t.Fatalf("agent.triage_model override: %q", cfg.Agent.TriageModel)
	}

	// Absent → keep the default.
	cfg2, err := loadFrom(Default(), "test.toml", []byte("[agent]\nmodel = \"opus\"\n"))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg2.Agent.TriageModel != "haiku" {
		t.Fatalf("agent.triage_model should keep default: %q", cfg2.Agent.TriageModel)
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
	if cfg.Agent.Thinking != "medium" {
		t.Fatalf("agent.thinking should keep its default: %q", cfg.Agent.Thinking)
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

// Default().Driver.Shell must be "" (auto) so a no-config run honours $SHELL.
func TestDefaultShellIsAuto(t *testing.T) {
	if got := Default().Driver.Shell; got != "" {
		t.Fatalf("Driver.Shell default = %q, want \"\" (auto/honor-$SHELL)", got)
	}
}

// A [driver] shell key in the config overrides the default.
func TestDriverShellMergeOverride(t *testing.T) {
	data := []byte("[driver]\nshell = \"bash\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Driver.Shell != "bash" {
		t.Fatalf("Driver.Shell override: got %q, want bash", cfg.Driver.Shell)
	}
}

// A config that sets only [agent] (no [driver]) keeps the default "" (auto) shell.
func TestDriverShellAbsentKeepsAuto(t *testing.T) {
	data := []byte("[agent]\nharness = \"pi\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Driver.Shell != "" {
		t.Fatalf("Driver.Shell should keep default: got %q, want \"\" (auto)", cfg.Driver.Shell)
	}
}

// MuxConfigured returns false for the baked-in Default (no user config, no [mux]).
func TestDefault_MuxConfiguredFalse(t *testing.T) {
	if Default().MuxConfigured() {
		t.Fatal("Default() must have MuxConfigured() == false")
	}
}

// MuxConfigured returns true after loading a config that includes a [mux] key.
func TestMuxConfigured_TrueWithMuxSection(t *testing.T) {
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
	if !cfg.MuxConfigured() {
		t.Fatal("config with [mux] section must have MuxConfigured() == true")
	}
}

// MuxConfigured returns false when only [agent] is configured (no [mux] section).
func TestMuxConfigured_FalseWithOnlyAgentSection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "ai-playbook")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[agent]\nharness = \"pi\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MuxConfigured() {
		t.Fatal("config with only [agent] section must have MuxConfigured() == false")
	}
}

// ── [store] section + store-dir resolver ─────────────────────────────────────

// Default GlobalStoreDir: no [store].global → derives from AI_PLAYBOOK_DATA_DIR+"/playbooks".
func TestGlobalStoreDir_Default(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", tmp)
	cfg := Default()
	got := cfg.GlobalStoreDir()
	want := tmp + "/playbooks"
	if got != want {
		t.Fatalf("GlobalStoreDir default = %q, want %q", got, want)
	}
}

// [store].global = "~/pb" → tilde expanded to $HOME/pb.
func TestGlobalStoreDir_TildeExpanded(t *testing.T) {
	data := []byte("[store]\nglobal = \"~/pb\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "pb")
	if got := cfg.GlobalStoreDir(); got != want {
		t.Fatalf("GlobalStoreDir tilde = %q, want %q", got, want)
	}
}

// [store].project = "/abs/pb" → returned verbatim by ProjectStoreDir.
func TestProjectStoreDir_Abs(t *testing.T) {
	data := []byte("[store]\nproject = \"/abs/pb\"\n")
	cfg, err := loadFrom(Default(), "test.toml", data)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got := cfg.ProjectStoreDir("/proj"); got != "/abs/pb" {
		t.Fatalf("ProjectStoreDir abs = %q, want /abs/pb", got)
	}
}

// Default project store: relative default joined onto projectRoot.
func TestProjectStoreDir_Default(t *testing.T) {
	cfg := Default()
	got := cfg.ProjectStoreDir("/proj")
	want := "/proj/.ai-playbook/playbooks"
	if got != want {
		t.Fatalf("ProjectStoreDir default = %q, want %q", got, want)
	}
}
