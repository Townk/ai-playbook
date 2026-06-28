package tools

import (
	"bufio"
	"encoding/json"
	"net"
	"time"
)

// Call is the harness-neutral request a client forwards to the backend (the same
// shape package-internal `request` decodes). Adapters (the MCP server, the future
// pi/cursor adapters) build a Call from their typed tool args and hand it to Dial.
type Call struct {
	Tool        string          `json:"tool"`
	ID          string          `json:"id,omitempty"`
	Cmd         string          `json:"cmd,omitempty"`
	Fact        string          `json:"fact,omitempty"`
	ProjectRoot string          `json:"projectRoot,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Type        string          `json:"type,omitempty"`
	Playbook    json.RawMessage `json:"playbook,omitempty"`
}

// Result is the backend's reply, decoded (the same shape package-internal `reply`
// encodes). Exit is the run exit code (-1 when never observed / on a transport
// error); OK is remember's success; Unavailable marks the deferred ask sentinel;
// Error carries a dispatch/transport error message.
type Result struct {
	Out         string `json:"out,omitempty"`
	Err         string `json:"err,omitempty"`
	Exit        int    `json:"exit"`
	OK          bool   `json:"ok,omitempty"`
	Answer      string `json:"answer,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	Error       string `json:"error,omitempty"`
}

// dialTimeout bounds connecting to the socket. The call itself is unbounded on
// the read (a `run` may legitimately take up to the backend's runTimeout); the
// backend always replies, so the read returns.
const dialTimeout = 5 * time.Second

// Dial connects to the tools backend at socketPath, sends one newline-framed JSON
// request, and returns the decoded reply. It is the single forwarding primitive
// every harness adapter uses: the MCP server's tool handlers build a Call and
// call Dial, so the wire protocol stays owned by this package.
func Dial(socketPath string, call Call) (Result, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return Result{Exit: -1}, err
	}
	defer conn.Close()

	line, err := json.Marshal(call)
	if err != nil {
		return Result{Exit: -1}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return Result{Exit: -1}, err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Result{Exit: -1}, err
		}
		return Result{Exit: -1}, errNoReply
	}
	var res Result
	if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
		return Result{Exit: -1}, err
	}
	return res, nil
}

// errNoReply is returned when the backend closed the connection without a reply.
var errNoReply = &dialError{"tools: backend closed connection without a reply"}

type dialError struct{ msg string }

func (e *dialError) Error() string { return e.msg }
