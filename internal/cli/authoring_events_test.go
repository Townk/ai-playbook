package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/launcher"
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
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir()) // deterministic empty KB

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

	reader, activity, fo := agentstream.FanOut(events, closeFn, launcher.ActivityBuffer)
	defer reader.Close()

	actCh := make(chan []string, 1)
	go func() { actCh <- drainActivity(activity) }()

	playbook, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read playbook: %v", err)
	}
	// The doc reaching the ui pipe is the authoritative Final/result text — the
	// rendered playbook — NOT the interim streamed narration. The streamed text
	// deltas arrive transiently on the activity channel instead.
	if string(playbook) != "# Diagnosis\nrun make test\n" {
		t.Errorf("playbook reaching the ui stream = %q, want the Final/result text", playbook)
	}

	gotAct := <-actCh
	foundTool, foundDelta := false, false
	for _, s := range gotAct {
		if s == "❯ make test" {
			foundTool = true
		}
		if s == "# Diagnosis\n" {
			foundDelta = true
		}
	}
	if !foundTool {
		t.Errorf("activity feed = %v, want it to include the tool line %q", gotAct, "❯ make test")
	}
	if !foundDelta {
		t.Errorf("activity feed = %v, want it to include the streamed text delta %q", gotAct, "# Diagnosis\n")
	}

	if fo.Body() != "# Diagnosis\nrun make test\n" {
		t.Errorf("cache body = %q, want the authoritative Final text", fo.Body())
	}
}
