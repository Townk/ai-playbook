// Package skills embeds the portable skill documents committed under
// skills/ so the binary can print and install them (the `skill` verb,
// internal/skillcmd). Each embedded document is pinned byte-identical to its
// repo file by a test in embed_test.go, so the shipped skill can never drift
// from the committed source.
package skills

import _ "embed"

// PlaybookAuthoring is skills/playbook-authoring/SKILL.md verbatim: the
// harness-agnostic playbook-authoring skill (schema quick-reference, the
// nine-rule quality rubric of docs/specifications/playbook-authoring.md, the
// worked bad-then-good example, and the validate iteration loop).
//
//go:embed playbook-authoring/SKILL.md
var PlaybookAuthoring []byte
