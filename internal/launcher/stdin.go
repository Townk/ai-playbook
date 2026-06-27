package launcher

import (
	"bufio"
	"fmt"
	"io"
	"strings"
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
