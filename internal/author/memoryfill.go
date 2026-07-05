package author

// WithMemoryFill appends the ONE wrap-up memory-fill instruction (ADR-0011 / K4):
// before finishing, the model should `remember` the session's durable lessons,
// classified per the two-set taxonomy. It is the write-time curation companion to
// the wrap-up prompt (FinalPlaybookPrompt) — the only authoring-shaped prompt that
// gets it.
//
// The instruction only makes sense when the MCP `remember` tool is actually wired,
// so the launcher applies this wrapper ONLY on the MCP-wired wrap-up (event) path;
// the text-fallback wrap-up and every other prompt shape leave it off. Like
// WithConstraints, it is a pure additive wrapper: the base prompt survives verbatim
// as a prefix, so an unwrapped prompt stays byte-identical to the pre-K4 output.
func WithMemoryFill(prompt string) string {
	return prompt + "\n\n" + memoryFillInstruction
}

// memoryFillInstruction is the fixed wrap-up addendum. The taxonomy guidance mirrors
// StructuredToolInstruction's `remember` sentence, and the "never secrets or env
// dumps" rule is preserved verbatim (spec "remember tool" — the existing rule stays).
const memoryFillInstruction = "## Before you finish — remember what you learned\n" +
	"You have the MCP `remember` tool. Before you finish, save the session's durable " +
	"lessons as facts with `remember`, each classified with `kind` by how closely it is " +
	"tied to the topic at hand: `system` for machine/tooling truths, `user` for who the " +
	"user is or prefers, `environment` for this project's setup, `topic` (with a `topic` " +
	"name) for a domain-specific lesson. Save only durable, reusable lessons — skip this " +
	"when there is nothing worth keeping. Never save secrets or raw environment dumps."
