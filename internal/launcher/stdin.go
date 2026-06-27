package launcher

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/tools"
)

// readRequestStdin prompts on out ("Request: "), reads one line from in, trims
// whitespace, and returns (text, true). Returns ("", false) on an empty/blank line
// or EOF — there is nothing useful to submit to the session.
func readRequestStdin(in io.Reader, out io.Writer) (string, bool) {
	fmt.Fprint(out, "Request: ")
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		s := strings.TrimSpace(scanner.Text())
		if s != "" {
			return s, true
		}
	}
	return "", false
}

// stdinAsk returns a tools.AskFunc that prints req.Prompt to out (when non-empty)
// and reads the answer from in. The scanner is created once and shared across calls
// so multiple consecutive asks consume the stream sequentially without fighting over
// the internal read buffer.
//
// Returns io.EOF when the reader is exhausted before an answer arrives, so the
// agent can detect the unavailability and degrade gracefully.
func stdinAsk(in io.Reader, out io.Writer) tools.AskFunc {
	scanner := bufio.NewScanner(in)
	return func(req floatinput.Request) (floatinput.Result, error) {
		if req.Prompt != "" {
			fmt.Fprintln(out, req.Prompt)
		}
		if scanner.Scan() {
			return floatinput.Result{Value: scanner.Text(), Submitted: true}, nil
		}
		if err := scanner.Err(); err != nil {
			return floatinput.Result{}, err
		}
		return floatinput.Result{}, io.EOF
	}
}
