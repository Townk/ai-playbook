package mux

import "errors"

// ErrUnavailable is returned by null's spawn/type methods when no multiplexer
// is present. Callers check for it via errors.Is to take an inline fallback.
var ErrUnavailable = errors.New("mux: no multiplexer available")

// null is the no-op Mux selected when no multiplexer is detected. DumpScreen
// returns an empty string (capture tolerates an empty scrollback); all spawn
// and type-into methods return ErrUnavailable so callers can detect the
// absence and take an appropriate inline fallback path.
type null struct{}

// Null returns a Mux that does nothing. It is selected automatically when the
// runtime environment has no terminal multiplexer. Callers that need to
// distinguish this from a real Mux use IsNull.
func Null() Mux { return null{} }

// IsNull reports whether m is the no-op null Mux returned by Null. Use this
// to gate code that cannot proceed without a live multiplexer.
func IsNull(m Mux) bool {
	_, ok := m.(null)
	return ok
}

func (null) DumpScreen(string) (string, error)  { return "", nil }
func (null) SpawnFloat(SpawnOptions) error      { return ErrUnavailable }
func (null) SpawnInputFloat(SpawnOptions) error { return ErrUnavailable }
func (null) SpawnPane(SpawnOptions) error       { return ErrUnavailable }
func (null) SpawnDocked(SpawnOptions) error     { return ErrUnavailable }
func (null) TypeInto(string, string) error      { return ErrUnavailable }
