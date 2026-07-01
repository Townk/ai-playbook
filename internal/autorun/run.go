package autorun

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/orchestrator"
)

// RunConfig is the launcher's headless-run request.
type RunConfig struct {
	Blocks       []Block
	EnvVars      map[string]frontmatter.EnvValue // declared front-matter env (var → {value,why})
	Cwd          string
	Shell        string // driver.Options.Shell selector
	Slug         string
	AutoRollback bool
	Out          io.Writer     // default os.Stdout when nil
	Now          func() string // timestamp source; default UTC "20060102T150405Z"
}

// noopMux is a package-local orchestrator.Mux for headless runs — there is no
// terminal-multiplexer pane to copy/play into, so both are no-ops.
type noopMux struct{}

func (noopMux) Copy(string) error { return nil }
func (noopMux) Play(string) error { return nil }

// resolveEnv computes the preflighted env slice for the driver, per rc.EnvVars.
// For each declared var: resolved = os.Getenv(name) if set, else ev.Value; if
// resolved is "" the var is required-and-missing. missing holds (name, why)
// pairs for every missing var, in map-iteration order.
func resolveEnv(vars map[string]frontmatter.EnvValue) (env []string, missing []struct{ name, why string }) {
	env = os.Environ()
	existing := make(map[string]bool, len(env))
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				existing[e[:i]] = true
				break
			}
		}
	}

	for name, ev := range vars {
		resolved := os.Getenv(name)
		if resolved == "" {
			resolved = ev.Value
		}
		if resolved == "" {
			missing = append(missing, struct{ name, why string }{name, ev.Why})
			continue
		}
		if !existing[name] {
			env = append(env, name+"="+resolved)
		}
	}
	return env, missing
}

// orchRunner is the orchestrator-backed StepRunner: it maps autorun's Step
// kinds onto orchestrator.Action and drives the real (or pty) shell via Do.
type orchRunner struct {
	orch *orchestrator.Orchestrator
	out  io.Writer
}

// kindFor maps an autorun StepKind onto its orchestrator.Kind equivalent.
func kindFor(k StepKind) orchestrator.Kind {
	switch k {
	case KindApplyDiff:
		return orchestrator.KindApplyDiff
	case KindCreateFile:
		return orchestrator.KindCreateFile
	default:
		return orchestrator.KindRun
	}
}

// RunStep executes one step via the orchestrator, streams its output to
// r.out, and captures it to a temp log file (mirrors internal/ui's
// writeRunLog shape; kept private here to avoid importing internal/ui).
func (r *orchRunner) RunStep(s Step) (exit int, outputPath string) {
	res, _ := r.orch.Do(orchestrator.Action{Kind: kindFor(s.Kind), ID: s.ID, Payload: s.Command})

	if res.Out != "" {
		fmt.Fprint(r.out, res.Out)
	}
	if res.Err != "" {
		fmt.Fprint(r.out, res.Err)
	}

	return res.Exit, writeStepLog(s.ID, res.Out, res.Err)
}

// writeStepLog writes a step's captured stdout then stderr to a temp file and
// returns its path. On any error it returns "" — an empty logpath is treated
// as "no log" by callers.
func writeStepLog(id, out, errOut string) string {
	f, err := os.CreateTemp("", "apb-run-"+sanitizeStepID(id)+"-*.log")
	if err != nil {
		return ""
	}
	defer f.Close()
	if out != "" {
		_, _ = f.WriteString(out)
		if errOut != "" {
			_, _ = f.WriteString("\n")
		}
	}
	if errOut != "" {
		_, _ = f.WriteString(errOut)
	}
	return f.Name()
}

// sanitizeStepID keeps a step id safe for a filename: non-[A-Za-z0-9_-] → _.
func sanitizeStepID(id string) string {
	b := []byte(id)
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			b[i] = '_'
		}
	}
	return string(b)
}

// newOrchRunner opens a real driver + headless orchestrator for rc and wraps
// them in an orchRunner. The returned cleanup closes the driver; callers must
// invoke it (e.g. via defer) once done. Factored out so Task 5's interrupt
// path can share the same driver/orch construction as Run.
func newOrchRunner(rc RunConfig, out io.Writer, env []string) (*orchRunner, func(), error) {
	d, err := driver.Open(driver.Options{Cwd: rc.Cwd, Shell: rc.Shell, Env: env})
	if err != nil {
		return nil, nil, err
	}
	orch := orchestrator.New(d, noopMux{})
	cleanup := func() { _ = d.Close() }
	return &orchRunner{orch: orch, out: out}, cleanup, nil
}

// defaultStamp formats now() as the UTC "20060102T150405Z" run-log timestamp.
func defaultStamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

// Run resolves + preflights env, opens a driver + orchestrator (headless, no
// float/mux), and executes rc.Blocks via Execute. Returns the process exit
// code. Missing required env vars are printed (name + why) and cause a
// non-zero return BEFORE the driver is opened.
func Run(rc RunConfig) int {
	out := rc.Out
	if out == nil {
		out = os.Stdout
	}
	now := rc.Now
	if now == nil {
		now = defaultStamp
	}

	env, missing := resolveEnv(rc.EnvVars)
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Fprintf(out, "missing required env: %s — %s\n", m.name, m.why)
		}
		return 1
	}

	runner, cleanup, err := newOrchRunner(rc, out, env)
	if err != nil {
		fmt.Fprintf(out, "failed to open driver: %v\n", err)
		return 1
	}
	defer cleanup()

	return Execute(Config{
		Blocks:       rc.Blocks,
		AutoRollback: rc.AutoRollback,
		Out:          out,
		LogDir:       cache.DefaultRoot(),
		Stamp:        now(),
		Slug:         rc.Slug,
	}, runner)
}
