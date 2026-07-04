package ui

import (
	"fmt"
	"os"
	"path/filepath"

	"testing"

	"github.com/Townk/ai-playbook/pkg/driver"
)

// sharedDriver is one *driver.Driver opened for the whole package's test run and
// reused by helpers that only need SOME driver to construct an
// orchestrator.Orchestrator (they exercise fakeAgent/fakeEventsProducer/synthetic
// resultMsg values and never run a real command through it). driver.Open's
// ready() has a hardcoded 1200ms idle floor, and this package used to pay that
// ~84 times (once per helper call) — opening it once here cuts the suite from
// ~156s to under 20s. Helpers that genuinely run real shell commands (e.g.
// newInProcModel) still open their own fresh driver — see inprocess_test.go.
var sharedDriver *driver.Driver

// TestMain opens sharedDriver once before any test in the package runs and closes
// it once after they all finish. t.TempDir() isn't available outside a test, so
// the ZDOTDIR scratch dir is made with os.MkdirTemp and removed after Close.
func TestMain(m *testing.M) {
	zdot, err := os.MkdirTemp("", "apb-ui-shared-zdotdir-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "main_shared_test: MkdirTemp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(zdot)

	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "main_shared_test: write .zshrc: %v\n", err)
		os.Exit(1)
	}

	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "main_shared_test: driver.Open: %v\n", err)
		os.Exit(1)
	}
	sharedDriver = d

	code := m.Run()

	sharedDriver.Close()
	os.Exit(code)
}
