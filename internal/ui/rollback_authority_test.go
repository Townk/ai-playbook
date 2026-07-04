package ui

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/playbook"
)

// TestCollectRollbackTargets_TopLevelOnly pins the schema rule for rollback=
// authority (ADR-0009 step 2): only a TOP-LEVEL block's rollback= counts, exactly
// as playbook.ParseBlocks / validate / run see the document. A rollback= tag on code
// nested inside a list or blockquote is prose-only and MUST be inert — it can no
// longer suppress the target's run button, so the renderer and `validate` can never
// disagree about which blocks are rollback commands.
func TestCollectRollbackTargets_TopLevelOnly(t *testing.T) {
	md := "# P\n\n" +
		// TOP-LEVEL rollback= → its target ("setup") is authoritative.
		"```bash {id=undo-top rollback=setup}\nrm -rf build\n```\n\n" +
		// rollback= on code NESTED in a list item → prose-only, must be inert.
		"- a nested step:\n\n" +
		"    ```bash {id=undo-nested rollback=deploy}\nkubectl rollout undo\n```\n"

	got := collectRollbackTargets(playbook.ParseBlocks(md))

	if !got["setup"] {
		t.Errorf("a top-level rollback= must mark its target; got %v", got)
	}
	if got["deploy"] {
		t.Errorf("rollback= on code nested in a list must be inert (top-level-only rule); got %v", got)
	}
}
