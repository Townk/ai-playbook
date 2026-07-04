package author

import "strings"

// WithConstraints appends the session's user-rejected-approach reasons to a
// re-engagement prompt as a bounded "Constraints" section, so every subsequent
// re-authoring is steered away from an approach the user already refused.
//
// It is the single injection seam shared by all re-engagement kinds (regenerate,
// followup, final-playbook, drift-regen): the launcher builds the per-kind system
// prompt, then wraps it here before handing it to the harness.
//
// Contract:
//   - An empty, nil, or all-blank list returns prompt UNCHANGED (byte-identical) —
//     so with no active constraint every prompt is exactly what it was before this
//     feature (characterization-stable).
//   - Otherwise the section is appended after a blank line. Each entry is trimmed;
//     blank entries are skipped. The heading + lead-in are fixed text; the reasons
//     become a bullet list, interpolated verbatim (same trust model as the `f`
//     change note — no escaping beyond trimming).
func WithConstraints(prompt string, constraints []string) string {
	var bullets []string
	for _, c := range constraints {
		if c = strings.TrimSpace(c); c != "" {
			bullets = append(bullets, "- "+c)
		}
	}
	if len(bullets) == 0 {
		return prompt
	}
	section := "## Constraints (user-rejected approaches)\n" +
		"The user explicitly rejected the following. Do NOT propose them again, in this or any alternative form:\n" +
		strings.Join(bullets, "\n")
	return prompt + "\n\n" + section
}
