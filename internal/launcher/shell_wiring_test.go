package launcher

import (
	"errors"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// TestOpenSession_ThreadsConfiguredShell is the regression guard that the
// configured shell (cfg.Driver.Shell, threaded through runSession → openSession)
// actually reaches the runtime driver as driver.Options.Shell. Before this wiring
// the assist/escalate path hardcoded Options{Shell:""} so `[driver] shell = "bash"`
// was inert. The driverOpen seam captures the Options without spawning a live shell.
func TestOpenSession_ThreadsConfiguredShell(t *testing.T) {
	cases := []struct {
		name  string
		shell string
		want  string
	}{
		// The bug: a configured non-default shell must propagate to the driver.
		{name: "bash propagates", shell: "bash", want: "bash"},
		{name: "sh propagates", shell: "sh", want: "sh"},
		// No-regression: the zsh default ("") must stay "" (driver resolves zsh).
		{name: "empty preserves zsh default", shell: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got driver.Options
			captured := false
			orig := driverOpen
			driverOpen = func(o driver.Options) (*driver.Driver, error) {
				got = o
				captured = true
				// Return an error so openSession degrades to nil without needing a
				// live shell — we only care that the Options carried the shell.
				return nil, errors.New("stub: driver not opened in test")
			}
			defer func() { driverOpen = orig }()

			sess := openSession(capture.Request{ProjectRoot: t.TempDir()}, mux.Null(), nil, tc.shell)
			if sess != nil {
				t.Fatal("openSession should degrade to nil when driverOpen errors")
			}
			if !captured {
				t.Fatal("driverOpen seam was never called")
			}
			if got.Shell != tc.want {
				t.Errorf("driver.Options.Shell = %q, want %q", got.Shell, tc.want)
			}
		})
	}
}
