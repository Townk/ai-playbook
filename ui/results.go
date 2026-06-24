package ui

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type resultMsg struct{ ID string; Exit int; Logpath string }

type blockRunState struct {
	Status    string // "running" | "ok" | "failed" | "stopped"
	Action    string // "apply" | "undo" | "run" — which action the in-flight result belongs to
	Exit      int
	Logpath   string
	Expanded  bool
	SpinFrame int
	Stopped   bool // user clicked stop on this block; suppress auto-followup when its result arrives
}

// parseResults reads <id>\x1f<exit>\x1f<logpath>\x1e records until EOF, calling
// send(resultMsg) for each. Runs in main's goroutine; send is Program.Send.
func parseResults(r io.Reader, send func(tea.Msg)) {
	sc := bufio.NewScanner(r)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == 0x1e {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\x1f", 3)
		if len(f) != 3 {
			continue
		}
		code, _ := strconv.Atoi(f[1])
		send(resultMsg{ID: f[0], Exit: code, Logpath: f[2]})
	}
}
