package launcher

import (
	"errors"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/reengage"
)

// driftRegenReengage builds the minimal re-engagement context a `run --file` viewer
// needs to regenerate a DRIFTED diff block against its current on-disk file. A plain
// standalone playbook otherwise has no agent wired, so DriftRegen would short-circuit
// to "regenerate unavailable" — this attaches the harness (config Agent.Harness,
// "claude" by default) for that ONE action.
//
// It is marked DriftRegenOnly so the viewer's authoring-grade affordances (the
// "try another fix" followup, whole-playbook regenerate) stay OFF — this context
// supports drift regenerate and nothing else. The harness is invoked lazily on click;
// if its binary is not on PATH, author.RunHarnessEvents returns an "executable file
// not found" error, which the viewer surfaces as a clear "no AI backend" note (F24).
// No harness picker: one supported harness today → use it; detection of a second is a
// later concern.
//
// projectRoot is the playbook's resolved project root when the source is
// project_bound (runFile resolves it into ui.Options.ProjectRoot before wiring
// this), "" for a non-project-bound file. Recall threads it so a project-bound
// drift regen sees the project Environment/Topics set too; "" recalls the
// global set only.
func driftRegenReengage(projectRoot string) *reengage.Reengage {
	return &reengage.Reengage{
		DriftRegenOnly: true,
		Events: func(kind reengage.ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error) {
			if kind != reengage.KindReengageDriftRegen {
				// A DriftRegenOnly context is never asked for another kind (the followup
				// affordances are gated off), but fail loudly rather than silently if it is.
				return nil, nil, errors.New("run --file re-engagement supports drift regenerate only")
			}
			cfg, _ := config.Load()
			sys, user := driftRegenPrompts(base, change, constraints, projectRoot, cfg)
			return author.RunHarnessEvents(sys, user, author.AuthorOptions{Cfg: cfg})
		},
	}
}

// driftRegenPrompts is the pure prompt assembly for the standalone drift-regen
// path (testable without spawning the harness): recall both knowledge sets
// (tail-capped at the load boundary; projectRoot "" ⇒ global set only), build the
// drift prompt over base = the current file content and change = the stale patch
// (DriftRegen's args), then fold the session constraints (refuse-solution §1)
// like the other kinds — nil/empty leaves the prompt byte-identical. A standalone
// `run --file` viewer has no refusal UI, so the list is nil in practice.
func driftRegenPrompts(base, change string, constraints []string, projectRoot string, cfg *config.Config) (sys, user string) {
	global, project := author.LoadRecall(cfg.KBDir(), projectRoot, cfg.KB.Budget)
	sys, user = author.DriftRegenPrompt(base, change, global, project)
	return author.WithConstraints(sys, constraints), user
}
