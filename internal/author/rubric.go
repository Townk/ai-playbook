package author

// authoringRubric is the single-source authoring quality bar: a prompt-voice
// distillation of the nine rules in docs/specifications/playbook-authoring.md
// ("The rubric"). SystemPrompt embeds it VERBATIM, and every authoring
// composition builds on SystemPrompt — the markdown path appends ToolInstruction,
// the structured create path appends StructuredToolInstruction (RunHarnessEvents)
// — so BOTH paths teach the SAME quality bar exactly ONCE and cannot drift
// (rubric_test.go pins the once-per-composition count). The spec is the source
// of truth: edit it first, then mirror the change here (Q3's SKILL derives from
// the same source).
const authoringRubric = `## The authoring rubric — the quality bar

A good playbook is a sequence of CHECKPOINTED, individually confirmable steps
that a person (or ` + "`--auto`" + `) can run, verify, and undo. Hold every playbook to
these rules:

1. ATOMICITY — one logical step per block. A block does ONE thing a human
   confirms once (install one component, write one config, start one service).
   If describing a block needs an "and then…", split it. Group multiple shell
   commands ONLY when they form one atomic action (` + "`mkdir && cd && tar`" + `),
   never when they are separate steps.
2. CREATE FILES WITH ` + "`file=`" + `. A new file's FULL content goes in a create block
   (` + "`file=<path>`" + `) — NEVER a shell block with a heredoc / ` + "`cat >`" + ` / ` + "`tee`" + `. A
   create block is previewable, undoable, and diffable; a heredoc is none of these.
3. EDIT WITH A DIFF BLOCK. Change an existing file with a diff block — a
   complete, ` + "`git apply`" + `-able unified diff (` + "`--- a/…`" + ` / ` + "`+++ b/…`" + ` headers, real
   ` + "`@@`" + ` hunks, paths relative to the project root) — never a ` + "`sed -i`" + ` one-liner
   for a structural change, never a rewrite-the-whole-file heredoc.
4. ROLLBACK EVERY MUTATION. Each step that MUTATES state (installs, writes,
   enables, registers) declares ` + "`rollback=<undo-id>`" + ` in its fence tag, naming a
   companion ` + "`{id=<undo-id>}`" + ` block that restores the pre-step state; on
   failure, completed steps' rollbacks run in REVERSE order, each undoing only
   its own step. Read-only checks and queries need none.
5. VERIFY, ALWAYS. End with a single ` + "`verify`" + ` block proving the GOAL state:
   a troubleshooting playbook re-runs the originally failing command; a how-to
   or onboarding playbook checks the installed / configured / running state.
6. DECLARE REAL DEPENDENCIES. Use ` + "`needs=`" + ` for ordering (B requires A succeeded)
   and ` + "`from=`" + ` for data (B consumes A's output on stdin). Do NOT serialize steps
   that are actually independent.
7. MARK ILLUSTRATION ` + "`static`" + `. Sample output, expected trees, and captured
   errors are ` + "`static`" + ` (non-runnable) blocks — never runnable.
8. PORTABILITY + ` + "`env:`" + `. Declare every required variable in the ` + "`env:`" + ` front
   matter (name + why); reach machine-specific locations through ` + "`$PROJECT_ROOT`" + `,
   ` + "`$HOME`" + `, and tool-resolved variables instead of hardcoding them. The playbook
   must run on a machine that is not the author's.
9. CALLOUTS FOR DANGER. A ` + "`warning`" + ` or ` + "`caution`" + ` callout PRECEDES every
   destructive or irreversible step.`

// AuthoringRubric returns the shared rubric fragment (the nine rules of
// docs/specifications/playbook-authoring.md in prompt voice) for consumers
// outside the authoring compositions — the launcher's AI review pass embeds
// it so review judges against the exact quality bar the authoring prompts
// teach.
func AuthoringRubric() string { return authoringRubric }
