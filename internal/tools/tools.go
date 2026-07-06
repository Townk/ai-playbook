// Package tools is the agent-tools backend: a small, harness-neutral server the
// session owner exposes over a unix socket so a headless authoring agent can
// diagnose in the USER's environment (their interactive shell via our driver)
// rather than the harness's own bash.
//
// Why a socket: the authoring agent (claude, for now) runs as a separate process
// the harness controls; it can't reach our in-process *driver.Driver directly. We
// expose the driver (and the KB / ask channels) over a unix socket speaking a
// tiny, line-delimited JSON RPC, and each harness reaches it through a thin
// adapter. For claude that adapter is MCP (`ai-playbook mcp --socket <path>`,
// package mcpserver) — but the wire protocol here is harness-neutral so the
// pi-extension and cursor-mcp adapters (later) speak the same thing.
//
// Wire protocol (one JSON object per line, request and reply both newline-framed):
//
//	→ {"tool":"run","id":"fix","cmd":"gg build"}
//	← {"out":"…","err":"…","exit":0}
//
//	→ {"tool":"remember","fact":"deploys via fly.io","projectRoot":"/p"}
//	← {"ok":true}
//
//	→ {"tool":"ask","prompt":"which env?","type":"line"}
//	← {"answer":"prod"}                        (submitted)
//	← {"answer":"","unavailable":true,"error":"…"}   (no float backend / cancelled)
//
// Tools:
//   - run      — execute a command in the session shell via *driver.Driver.RunID;
//     the load-bearing one (the agent runs `gg build` etc. in the user's env).
//   - remember — classify a distilled fact into the two-set taxonomy (kb.Append):
//     system/user → the global KB; environment/topic → the project KB.
//   - ask      — the user-input channel. When the session wired an Asker (the
//     float plumbing — selfExe + mux), `ask` spawns an input FLOAT and returns
//     the user's submitted answer; on cancel it returns the unavailable sentinel
//     so the agent gets a definite answer rather than hanging. With no Asker (the
//     no-mux fallback) it returns the unavailable sentinel directly.
package tools

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// runTimeout bounds a single agent-issued `run`. Deliberately this tool's OWN
// 120s ceiling (the broker-era AI_PLAYBOOK_RUN_TIMEOUT default): an
// authoring-time probe should fail fast, so it does NOT track the
// orchestrator's 10-minute block-run default (nor a block's timeout= — no
// parsed block exists at this stage).
const runTimeout = 120 * time.Second

// askUnavailableMsg is the sentinel reply when an ask cannot be completed: the
// user cancelled (no submitted answer), the ask type is unsupported, or no Ask
// backend is wired (unsupported context). The no-mux interactive path wires the
// AskBridge overlay; nil Deps.Ask means no backend at all.
const askUnavailableMsg = "interactive ask not available in this context"

// AskFunc spawns an input float and returns the user's answer. It is the seam
// the `ask` tool drives: the session wires it from a floatinput.Asker (the real
// float plumbing); tests inject a fake. A nil AskFunc means no interactive ask is
// available (the no-mux fallback → unavailable sentinel).
type AskFunc func(req floatinput.Request) (floatinput.Result, error)

// Deps carry what the backend needs to service tool calls: the session's live
// shell driver (RunID for `run`), the project root (the KB key + the default
// `remember` target), the KB data-dir root (kb.Append target; empty →
// kb.DefaultRoot, the real data dir), the cwd ask-floats open in, and the Ask
// seam (nil → ask is unavailable).
type Deps struct {
	Driver      *driver.Driver
	ProjectRoot string
	KBRoot      string // kb data-dir root; "" → kb.DefaultRoot()
	Cwd         string // working dir an ask-float opens in (the request's project root)
	Ask         AskFunc
	// OnPlaybook, when set, receives a validated structured playbook submitted via
	// the submit_playbook tool. nil → submit_playbook replies "unavailable".
	OnPlaybook func(pb draft.Playbook)
	// ValidateFileBlocks, when set, is called after schema validation and before
	// OnPlaybook: it rejects the submission (as reply.Error) if any file= block
	// targets a path that already exists in the project (the model should use a diff
	// block to edit existing files). nil → skipped. The FS/project-root logic lives
	// in the launcher, which injects it here so internal/tools stays FS-decoupled.
	ValidateFileBlocks func(pb draft.Playbook) error
}

// request is the inbound RPC: tool selector + the union of per-tool fields.
type request struct {
	Tool        string          `json:"tool"`
	ID          string          `json:"id,omitempty"`          // run: block id for value-passing (APB_OUT_<id>)
	Cmd         string          `json:"cmd,omitempty"`         // run: the command line
	Kind        string          `json:"kind,omitempty"`        // remember: system|user|environment|topic
	Topic       string          `json:"topic,omitempty"`       // remember: required iff kind=topic
	Fact        string          `json:"fact,omitempty"`        // remember: the distilled fact
	ProjectRoot string          `json:"projectRoot,omitempty"` // remember: override target (else Deps.ProjectRoot); global kinds reject a non-empty override
	Prompt      string          `json:"prompt,omitempty"`      // ask: the question
	Type        string          `json:"type,omitempty"`        // ask: free|line|confirm|choose
	Playbook    json.RawMessage `json:"playbook,omitempty"`    // submit_playbook: the structured playbook
}

// reply is the outbound RPC: the union of per-tool result fields. Unused fields
// are omitted so each tool's reply is a clean, minimal object.
type reply struct {
	// run
	Out  string `json:"out,omitempty"`
	Err  string `json:"err,omitempty"`
	Exit int    `json:"exit"`
	// remember
	OK bool `json:"ok,omitempty"`
	// ask (deferred float — see askUnavailableMsg)
	Answer      string `json:"answer,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	// transport/dispatch error (unknown tool, bad request)
	Error string `json:"error,omitempty"`
}

// Server is a running tools backend over a unix socket. Stop it with Close.
type Server struct {
	ln   net.Listener
	deps Deps

	mu     sync.Mutex
	closed bool
}

// Serve starts the tools backend listening on socketPath (a unix socket) and
// returns immediately; the accept loop runs in a goroutine. Stale socket files
// are removed first (the session owns the path). Close stops the listener and
// removes the socket. The caller (the session) owns the lifecycle: Serve once at
// session start, Close at teardown.
func Serve(socketPath string, deps Deps) (*Server, error) {
	if deps.Driver == nil {
		return nil, errors.New("tools.Serve: nil driver")
	}
	// The session owns this path; clear a stale socket from a crashed prior run.
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, deps: deps}
	go s.acceptLoop()
	return s, nil
}

// Close stops accepting connections and removes the socket file. Idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.ln.Close()
	_ = os.Remove(s.ln.Addr().String())
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			// Transient accept error: a brief pause avoids a hot spin.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		go s.handleConn(conn)
	}
}

// handleConn services one connection: it reads newline-framed JSON requests and
// writes one newline-framed JSON reply each, so a single connection can carry
// several tool calls (the MCP adapter dials per call, but the protocol allows
// reuse). A malformed line yields an {"error":…} reply and the connection
// continues.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // allow large run payloads
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(reply{Exit: -1, Error: "bad request: " + err.Error()})
			continue
		}
		_ = enc.Encode(s.dispatch(req))
	}
}

// dispatch routes one decoded request to its tool. Each handler returns the
// already-shaped reply.
func (s *Server) dispatch(req request) reply {
	switch req.Tool {
	case "run":
		return s.doRun(req)
	case "remember":
		return s.doRemember(req)
	case "ask":
		return s.doAsk(req)
	case "submit_playbook":
		return s.doSubmitPlaybook(req)
	default:
		return reply{Exit: -1, Error: "unknown tool: " + req.Tool}
	}
}

// doRun executes the command in the session shell via the driver (RunID, so the
// agent's block id value-passes APB_OUT_<id>/LAST_* like an authored run block).
// This runs in the USER's real environment — the whole point of the backend.
func (s *Server) doRun(req request) reply {
	res := s.deps.Driver.RunID(req.ID, req.Cmd, "", runTimeout)
	return reply{Out: res.Out, Err: res.Err, Exit: res.Exit}
}

// doRemember classifies a distilled fact into the two-set taxonomy and routes it
// via kb.Append: system/user land in the global KB, environment/topic in the
// project KB (the request's projectRoot override, else Deps.ProjectRoot). The
// data-dir root is Deps.KBRoot, else kb.DefaultRoot.
//
// The MCP jsonschema layer enforces `kind` as a required field (see
// mcpserver.rememberInput), but it cannot express the rest of the spec's
// contract — an unknown kind value, a topic required/rejected depending on
// kind, and a projectRoot override that is only valid for project kinds — so
// those are enforced here and reported as one-line tool errors:
//   - an unrecognized kind, a topic without kind=topic, or kind=topic without a
//     topic all surface as kb.Append's own contract-violation errors;
//   - a non-empty projectRoot override on a global kind (system/user) is
//     rejected before ever reaching kb.Append.
//
// Write-dedup (kb.Append) makes a repeated identical fact a silent no-op that
// still replies {ok:true}. An empty fact is likewise a no-op success.
func (s *Server) doRemember(req request) reply {
	kind := kb.Kind(req.Kind)
	if (kind == kb.KindSystem || kind == kb.KindUser) && req.ProjectRoot != "" {
		return reply{Error: "remember: projectRoot is only valid with kind=environment or kind=topic"}
	}
	root := s.deps.KBRoot
	if root == "" {
		root = kb.DefaultRoot()
	}
	proj := req.ProjectRoot
	if proj == "" {
		proj = s.deps.ProjectRoot
	}
	if err := kb.Append(root, proj, kind, req.Topic, req.Fact); err != nil {
		return reply{Error: err.Error()}
	}
	return reply{OK: true}
}

// doAsk is the user-input channel. With an Ask seam wired (the float plumbing on
// the mux-present path, or the bridge adapter on the no-mux interactive path),
// it returns the user's submitted answer; a cancel or unsubmitted answer returns
// the unavailable sentinel so the agent always gets a definite, non-hanging reply.
// Without an Ask seam (nil) it returns the sentinel directly (unsupported context).
func (s *Server) doAsk(req request) reply {
	if s.deps.Ask == nil {
		return reply{Unavailable: true, Error: askUnavailableMsg}
	}
	res, err := s.deps.Ask(floatinput.Request{
		Type:   req.Type,
		Title:  "ai-playbook",
		Prompt: req.Prompt,
		Cwd:    s.deps.Cwd,
	})
	if err != nil {
		return reply{Unavailable: true, Error: err.Error()}
	}
	if !res.Submitted {
		return reply{Unavailable: true, Error: askUnavailableMsg}
	}
	return reply{Answer: res.Value}
}

// doSubmitPlaybook decodes a structured playbook, validates it, and (on success)
// hands it to Deps.OnPlaybook. A validation failure is returned as reply.Error so
// the MCP adapter surfaces it as a tool error and the model re-submits.
func (s *Server) doSubmitPlaybook(req request) reply {
	if s.deps.OnPlaybook == nil {
		return reply{Error: "submit_playbook unavailable in this context"}
	}
	if len(req.Playbook) == 0 {
		return reply{Error: "submit_playbook requires a playbook field"}
	}
	var pb draft.Playbook
	if err := json.Unmarshal(req.Playbook, &pb); err != nil {
		return reply{Error: "could not parse playbook: " + err.Error()}
	}
	if err := draft.Validate(pb, false); err != nil {
		return reply{Error: err.Error()}
	}
	if s.deps.ValidateFileBlocks != nil {
		if err := s.deps.ValidateFileBlocks(pb); err != nil {
			return reply{Error: err.Error()}
		}
	}
	s.deps.OnPlaybook(pb)
	return reply{OK: true}
}
