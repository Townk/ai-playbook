package author

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ai-playbook/internal/config"
)

// MetadataPrompt must carry the JSON-only directive, the four schema fields
// (including importantEnvVars as {name, why}), and the doc itself.
func TestMetadataPrompt(t *testing.T) {
	const doc = "# Playbook — set up the Android build\n\n```bash {id=verify}\n./gradlew assembleDebug\n```\n"
	p := MetadataPrompt(doc)

	wants := []string{
		"JSON",                    // it's a JSON call
		"ONLY",                    // JSON-only directive
		"no markdown code\nfence", // no fence directive
		"description",
		"category",
		"tags",
		"importantEnvVars",
		`"name"`, // env var note name field
		`"why"`,  // env var note why field
		"imperative",
		doc, // the load-bearing input
	}
	for _, w := range wants {
		if !strings.Contains(p, w) {
			t.Errorf("MetadataPrompt missing %q\n--- prompt ---\n%s", w, p)
		}
	}
}

// fakeMetadataHarness writes a fake claude that emits a stream-json result whose
// `result` field is the given payload (the model's "final" text). The payload is
// embedded via the script so we control exactly what PlaybookMetadata must parse.
func fakeMetadataHarness(t *testing.T, resultText string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	// Marshal the whole result line as JSON so the payload (with quotes, braces,
	// newlines) is safely encoded into the NDJSON the script cats.
	line, err := json.Marshal(map[string]any{"type": "result", "result": resultText})
	if err != nil {
		t.Fatal(err)
	}
	// Single-quoted heredoc → the JSON line is emitted verbatim (no shell expansion).
	script := "#!/bin/sh\ncat <<'NDJSON'\n" + string(line) + "\nNDJSON\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-claude-meta")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runMeta drives PlaybookMetadata against a fake harness emitting resultText,
// capturing the owned argv so the no-MCP invariant can be asserted.
func runMeta(t *testing.T, resultText string) (Metadata, error, []string) {
	t.Helper()
	bin := fakeMetadataHarness(t, resultText)
	cfg := config.Default()
	cfg.Agent.Harness = "claude"

	var gotArgs []string
	meta, err := PlaybookMetadata("# Playbook — x\n\nbody\n", AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: "/tmp/should-be-ignored.json", // PlaybookMetadata must drop this
		Command: func(b string, args []string) *exec.Cmd {
			gotArgs = args
			return exec.Command(bin, args...)
		},
	})
	return meta, err, gotArgs
}

const cannedMetadataJSON = `{
  "description": "Set up the Android build from scratch",
  "category": "Android / build",
  "tags": ["android", "gradle", "build"],
  "importantEnvVars": [
    { "name": "ANDROID_HOME", "why": "SDK location the Gradle build resolves against" },
    { "name": "JAVA_HOME", "why": "JDK the Gradle toolchain uses" }
  ]
}`

func assertCannedMetadata(t *testing.T, meta Metadata) {
	t.Helper()
	if meta.Description != "Set up the Android build from scratch" {
		t.Errorf("description = %q", meta.Description)
	}
	if meta.Category != "Android / build" {
		t.Errorf("category = %q", meta.Category)
	}
	if strings.Join(meta.Tags, ",") != "android,gradle,build" {
		t.Errorf("tags = %v", meta.Tags)
	}
	if len(meta.ImportantEnvVars) != 2 {
		t.Fatalf("importantEnvVars len = %d, want 2: %+v", len(meta.ImportantEnvVars), meta.ImportantEnvVars)
	}
	if meta.ImportantEnvVars[0].Name != "ANDROID_HOME" ||
		meta.ImportantEnvVars[0].Why != "SDK location the Gradle build resolves against" {
		t.Errorf("env[0] = %+v", meta.ImportantEnvVars[0])
	}
	if meta.ImportantEnvVars[1].Name != "JAVA_HOME" ||
		meta.ImportantEnvVars[1].Why != "JDK the Gradle toolchain uses" {
		t.Errorf("env[1] = %+v", meta.ImportantEnvVars[1])
	}
}

// A clean JSON result parses into Metadata; the no-MCP invariant holds (the owned
// argv carries no --mcp-config even though MCPConfigPath was set), and the system
// prompt is MetadataPrompt(doc).
func TestPlaybookMetadata_ParsesCleanJSON(t *testing.T) {
	meta, err, args := runMeta(t, cannedMetadataJSON)
	if err != nil {
		t.Fatalf("PlaybookMetadata: %v", err)
	}
	assertCannedMetadata(t, meta)

	if strings.Contains(strings.Join(args, "\x00"), "--mcp-config") {
		t.Errorf("metadata call must NOT attach --mcp-config: %v", args)
	}
	wantSys := MetadataPrompt("# Playbook — x\n\nbody\n")
	if got := appendSystemPromptArg(args); got != wantSys {
		t.Errorf("metadata system prompt != MetadataPrompt(doc)\n--- got ---\n%s", got)
	}
}

// A ```json-fenced + whitespace-wrapped JSON result still parses (the parser
// extracts the outer {...}).
func TestPlaybookMetadata_ToleratesFenceAndWhitespace(t *testing.T) {
	wrapped := "\n\nHere is the metadata:\n```json\n" + cannedMetadataJSON + "\n```\n\n"
	meta, err, _ := runMeta(t, wrapped)
	if err != nil {
		t.Fatalf("PlaybookMetadata (fenced): %v", err)
	}
	assertCannedMetadata(t, meta)
}

// A non-JSON response is unparseable on both attempts → a clear error mentioning
// the classification failure. (The fake harness emits the same non-JSON each run,
// so the single retry also fails, exercising the retry-then-error path.)
func TestPlaybookMetadata_NonJSONErrorsAfterRetry(t *testing.T) {
	_, err, _ := runMeta(t, "Sorry, I can't classify this playbook right now.")
	if err == nil {
		t.Fatal("expected an error for non-JSON output")
	}
	if !strings.Contains(err.Error(), "classification failed after retry") {
		t.Errorf("error = %q, want a clear classification-failed-after-retry message", err)
	}
}
