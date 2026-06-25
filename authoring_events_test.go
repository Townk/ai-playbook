package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"ai-playbook/agentstream"
	"ai-playbook/author"
	"ai-playbook/capture"
	"ai-playbook/config"
)

// drainActivity reads the activity channel to close, returning everything it saw.
func drainActivity(ch <-chan string) []string {
	var got []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, s)
		case <-timeout:
			return got
		}
	}
}

// fakeClaudeStream emits canned claude stream-json: an init record, a text delta
// (the playbook), a tool_use (tool activity), and a final result.
const fakeClaudeStream = `#!/bin/sh
cat <<'NDJSON'
{"type":"system","subtype":"init"}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"# Diagnosis\n"}}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"run","input":{"command":"make test"}}]}}
{"type":"result","result":"# Diagnosis\nrun make test\n"}
NDJSON
`

// TestAuthorEventsFanOut_Integration drives the part-2a authoring boundary with a
// FAKE harness: AuthorEvents (via the injectable Command seam) emits canned
// stream-json, which the main-package fan-out splits into the playbook reader the
// ui consumes and the activity feed. Asserts the playbook text reaches the reader,
// the activity channel gets the tool line, and the cache body matches. (A headless
// ui can't render, so we assert at the fan-out/stream boundary — ui.RunStream's
// no-TTY path is exercised separately by the ui package.)
func TestAuthorEventsFanOut_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	t.Setenv("AI_ASSIST_DATA_DIR", t.TempDir()) // deterministic empty KB

	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(bin, []byte(fakeClaudeStream), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Agent.Harness = "claude"

	req := capture.Request{Kind: "error", Command: "make test", Exit: "1"}
	events, closeFn, err := author.AuthorEvents(req, author.AuthorOptions{
		Cfg: cfg,
		Command: func(_ string, args []string) *exec.Cmd {
			return exec.Command(bin, args...)
		},
	})
	if err != nil {
		t.Fatalf("AuthorEvents: %v", err)
	}

	reader, activity, fo := agentstream.FanOut(events, closeFn, activityBuffer)
	defer reader.Close()

	actCh := make(chan []string, 1)
	go func() { actCh <- drainActivity(activity) }()

	playbook, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read playbook: %v", err)
	}
	// Deltas streamed → the ui-facing pipe carries the streamed delta text; the
	// Final result is authoritative for the stored CACHE body, not the live stream.
	if string(playbook) != "# Diagnosis\n" {
		t.Errorf("playbook reaching the ui stream = %q, want the streamed delta", playbook)
	}

	gotAct := <-actCh
	foundTool := false
	for _, s := range gotAct {
		if s == "run: make test" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("activity feed = %v, want it to include the tool line %q", gotAct, "run: make test")
	}

	if fo.Body() != "# Diagnosis\nrun make test\n" {
		t.Errorf("cache body = %q, want the authoritative Final text", fo.Body())
	}
}
