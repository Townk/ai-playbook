package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
)

// Gated debug logging for the live launcher↔session↔float flow. These boundaries
// only misbehave under a real terminal multiplexer (panes/floats can't be tested
// headless), so we trace them to a file when AI_PLAYBOOK_DEBUG_LOG (launcher) or
// --debug-log (session pane — env may not survive the zellij spawn) is set.
var (
	dbgPath string
	dbgMu   sync.Mutex
)

func dbgInit(path string) { dbgPath = path }

// dbg appends a timestamped, pid-tagged line to the debug log if configured.
// Cheap no-op when unset. Append mode keeps the launcher and the spawned session
// pane interleaved in one file.
func dbg(format string, args ...any) {
	if dbgPath == "" {
		return
	}
	dbgMu.Lock()
	defer dbgMu.Unlock()
	f, err := os.OpenFile(dbgPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%d] %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, args...))
}

// dbgEnv records the facts behind the zellij-spawn env-drop hypothesis: whether
// the capable agent is resolvable in THIS process's PATH (a session pane spawned
// by `zellij action new-pane` inherits the zellij server's env, not the
// launcher's), and the PATH itself. The probed binary is the CONFIGURED
// harness's — the same author.HarnessBin resolution the real invocation uses
// (cfg [agent].bin else the harness name).
func dbgEnv(where string) {
	if dbgPath == "" {
		return
	}
	cfg, _ := config.Load()
	bin := author.HarnessBin(cfg)
	resolved, err := exec.LookPath(bin)
	dbg("%s: harness bin %q=%q lookErr=%v PATH=%q", where, bin, resolved, err, os.Getenv("PATH"))
}
