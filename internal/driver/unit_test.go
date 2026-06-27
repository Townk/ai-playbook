package driver

import "testing"

// TestSanitizeKey covers the value-passing key sanitizer: alnum + underscore are
// kept verbatim; every other byte (dash, dot, slash, space) becomes '_'.
func TestSanitizeKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abcXYZ_09", "abcXYZ_09"}, // all-allowed: unchanged
		{"a-b.c/d e", "a_b_c_d_e"}, // each disallowed byte → '_'
		{"", ""},                   // empty: unchanged
		{"!@#", "___"},             // all-disallowed
	}
	for _, c := range cases {
		if got := sanitizeKey(c.in); got != c.want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCwdAccessor asserts Cwd returns the initial working dir recorded at Open
// (Options.Cwd), and "" when none was set — without spawning a live shell.
func TestCwdAccessor(t *testing.T) {
	if got := (&Driver{cwd: "/tmp/x"}).Cwd(); got != "/tmp/x" {
		t.Errorf("Cwd() = %q, want /tmp/x", got)
	}
	if got := (&Driver{}).Cwd(); got != "" {
		t.Errorf("Cwd() = %q, want empty", got)
	}
}
