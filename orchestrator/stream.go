package orchestrator

import (
	"bytes"
	"io"
)

// storeOnClose wraps a stream, buffering every byte read from it, and runs onDone
// with the full buffered body when the stream is closed. It is the orchestrator's
// tee-on-completion seam for regenerate's re-store: the ui reads + renders the
// stream, and once it Closes (EOF reached, process reaped), the captured body is
// persisted to the cache. onDone runs exactly once.
type storeOnClose struct {
	io.ReadCloser
	buf    bytes.Buffer
	onDone func(body string)
	done   bool
}

func newStoreOnClose(rc io.ReadCloser, onDone func(body string)) *storeOnClose {
	return &storeOnClose{ReadCloser: rc, onDone: onDone}
}

func (s *storeOnClose) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if n > 0 {
		s.buf.Write(p[:n])
	}
	return n, err
}

func (s *storeOnClose) Close() error {
	err := s.ReadCloser.Close()
	if !s.done {
		s.done = true
		if s.onDone != nil {
			s.onDone(s.buf.String())
		}
	}
	return err
}

// teeCloser wraps a stream, mirroring every byte read into w (e.g. the solution
// artifact file), and closes extra (the same file) when the stream is closed. It
// is the wrap-up's tee-into-artifact seam: the ui reads + renders the stream while
// the bytes are simultaneously written to the artifact, then the artifact is
// closed on completion.
type teeCloser struct {
	io.ReadCloser
	w     io.Writer
	extra io.Closer
}

func (t *teeCloser) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 && t.w != nil {
		_, _ = t.w.Write(p[:n])
	}
	return n, err
}

func (t *teeCloser) Close() error {
	err := t.ReadCloser.Close()
	if t.extra != nil {
		_ = t.extra.Close()
	}
	return err
}
