package agentstream

import (
	"io"
	"strings"
)

// textAdapter is the passthrough / fallback adapter: it treats stdout as plain
// text with no structure. Every chunk read is emitted as a TextDelta, and at EOF
// it emits one Final carrying the full accumulated text. This preserves today's
// behavior for any harness or path that has no structured adapter.
type textAdapter struct{}

func (textAdapter) Parse(r io.Reader, emit func(Event)) error {
	var all strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			all.WriteString(chunk)
			emit(Event{Kind: TextDelta, Text: chunk})
		}
		if err != nil {
			if err == io.EOF {
				emit(Event{Kind: Final, Text: all.String()})
				return nil
			}
			return err
		}
	}
}
