package input

import (
	"reflect"
	"testing"
)

func TestEncodeDecodeRecord(t *testing.T) {
	got := encodeRecord("submit", "hello\nworld")
	if got != "submit\x1fhello\nworld\x1e" {
		t.Fatalf("encode = %q", got)
	}
	cmd, args := decodeRecord("status\x1fLooking up docs\x1e")
	if cmd != "status" || !reflect.DeepEqual(args, []string{"Looking up docs"}) {
		t.Fatalf("decode = %q %q", cmd, args)
	}
	// no-arg record
	cmd, args = decodeRecord("close\x1e")
	if cmd != "close" || len(args) != 0 {
		t.Fatalf("decode close = %q %q", cmd, args)
	}
}
