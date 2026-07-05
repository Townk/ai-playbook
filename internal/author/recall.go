package author

import (
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/kb"
)

// kbTailCapFactor is the multiple of the per-file [kb] budget at which recall
// applies its hard read-time tail-cap (spec "Recall": 8× budget). The cap is a
// vestigial safety against a pathologically hand-edited file, NOT the budget
// mechanism (that is write-time compaction, K4) — a normal file never trips it.
const kbTailCapFactor = 8

// LoadRecall is the single recall load boundary: it reads BOTH knowledge sets for
// projectRoot under root and applies the read-time tail-cap (kbTailCapFactor ×
// budget per file, keeping the head) via kb.Capped. Every authoring-shaped call
// recalls through here — the internal authors (Author / AuthorEvents / Followup /
// FinalPlaybookText) and the launcher's reengagePrompts — so recall is identical
// on all paths. A missing/empty file yields "".
func LoadRecall(root, projectRoot string, budget int) (global, project KnowledgeBase) {
	limit := budget * kbTailCapFactor
	global = KnowledgeBase(kb.Capped(kb.LoadGlobal(root), limit))
	project = KnowledgeBase(kb.Capped(kb.LoadProject(root, projectRoot), limit))
	return global, project
}

// recallFor loads both capped knowledge sets for projectRoot using cfg's [kb]
// settings (the resolved KBDir root + budget). A nil cfg falls back to
// config.Default(). It is the convenience wrapper the internal authors use; the
// launcher loads via LoadRecall directly so it can thread the same pair through
// several re-engagement builders in one call.
func recallFor(projectRoot string, cfg *config.Config) (global, project KnowledgeBase) {
	if cfg == nil {
		cfg = config.Default()
	}
	return LoadRecall(cfg.KBDir(), projectRoot, cfg.KB.Budget)
}
