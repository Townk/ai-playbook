package launcher

import (
	"errors"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/orchestrator"
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
func driftRegenReengage() *orchestrator.Reengage {
	return &orchestrator.Reengage{
		DriftRegenOnly: true,
		Events: func(kind orchestrator.ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error) {
			if kind != orchestrator.KindReengageDriftRegen {
				// A DriftRegenOnly context is never asked for another kind (the followup
				// affordances are gated off), but fail loudly rather than silently if it is.
				return nil, nil, errors.New("run --file re-engagement supports drift regenerate only")
			}
			cfg, _ := config.Load()
			// base = the current file content, change = the stale patch (DriftRegen's args).
			sys, user := author.DriftRegenPrompt(base, change)
			return author.RunHarnessEvents(sys, user, author.AuthorOptions{Cfg: cfg})
		},
	}
}
