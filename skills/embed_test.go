package skills

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestPlaybookAuthoring_MatchesRepoFile pins the embed to the committed
// source: the bytes the binary ships (and `skill show`/`skill install` emit)
// are exactly skills/playbook-authoring/SKILL.md. go:embed snapshots at
// build time, so this is the guard against a stale build artifact ever being
// asserted against an edited file (or vice versa).
func TestPlaybookAuthoring_MatchesRepoFile(t *testing.T) {
	disk, err := os.ReadFile("playbook-authoring/SKILL.md")
	if err != nil {
		t.Fatalf("read repo SKILL.md: %v", err)
	}
	if !bytes.Equal(disk, PlaybookAuthoring) {
		t.Errorf("embedded PlaybookAuthoring differs from skills/playbook-authoring/SKILL.md (%d vs %d bytes) — rebuild/re-test after editing the file", len(PlaybookAuthoring), len(disk))
	}
}

// TestPlaybookAuthoring_Frontmatter pins the skill's superpowers-style
// frontmatter: the name (the install directory is derived from it) and a
// trigger description.
func TestPlaybookAuthoring_Frontmatter(t *testing.T) {
	s := string(PlaybookAuthoring)
	for _, want := range []string{
		"name: playbook-authoring",
		"description: Use when authoring an ai-playbook runnable playbook",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md frontmatter missing %q", want)
		}
	}
}

// TestPlaybookAuthoring_CoversNineRules asserts the SKILL body teaches each
// of the nine rubric rules (docs/specifications/playbook-authoring.md) via
// the same keyword coverage internal/author's rubric_test.go asserts on the
// prompt fragment — both artifacts derive from the same spec, and this is
// the consistency tie that catches a rule silently dropped from one of them.
//
// Beyond the shared keyword set, each rule also pins one DISTINCTIVE anchor
// phrase that only its rule text satisfies: generic tokens like needs=,
// from=, static, or heredoc also occur in the fence-tag table, the checklist,
// and the worked example, so without an anchor a rule could be deleted
// wholesale from the rubric section and this test would still pass.
func TestPlaybookAuthoring_CoversNineRules(t *testing.T) {
	s := string(PlaybookAuthoring)
	cases := []struct {
		rule  string
		wants []string
	}{
		{"1 atomicity", []string{"one logical step per block", "never when they are separate steps"}},
		{"2 file=-not-heredoc", []string{"file=", "file=<path>", "heredoc", "previewable, undoable, and diffable"}},
		{"3 diff-for-edits", []string{"diff block", "unified diff", "paths relative to the project root", "rewrite-the-whole-file heredoc"}},
		{"4 rollback-per-mutating-step", []string{"rollback", "MUTATES", "restores the pre-step state"}},
		{"5 verify-always-both-kinds", []string{"verify", "troubleshooting", "how-to", "One block, one authoritative check"}},
		{"6 needs/from distinction", []string{"needs=", "from=", "serialize independent steps"}},
		{"7 static", []string{"static", "Sample output, expected trees"}},
		{"8 env+portability", []string{"env:", "$PROJECT_ROOT", "$HOME", "runnable on a machine that is not the author's"}},
		{"9 callouts", []string{"callout", "warning", "caution", "or irreversible step"}},
	}
	for _, tc := range cases {
		for _, w := range tc.wants {
			if !strings.Contains(s, w) {
				t.Errorf("rubric rule %s: SKILL.md missing keyword %q", tc.rule, w)
			}
		}
	}
}

// skillRollbackValueRe extracts the value of every rollback= occurrence in
// the SKILL (placeholders and worked-example fence tags alike).
var skillRollbackValueRe = regexp.MustCompile(`rollback=([A-Za-z0-9_<>-]+)`)

// TestPlaybookAuthoring_RollbackDirection pins the rollback= teaching to the
// CORRECT direction (the schema contract, corrected 2026-07-05): the FORWARD
// step declares rollback=<undo-id> in its fence tag, naming a companion
// {id=<undo-id>} block — rollback= is NEVER a tag on the undo block. The
// value scan enforces it structurally: every rollback= value in the document
// must be the <undo-id> placeholder or an undo-* id from the worked example;
// a rollback= value naming a forward step (rollback=install) would be the
// reversed, wrong direction.
func TestPlaybookAuthoring_RollbackDirection(t *testing.T) {
	s := string(PlaybookAuthoring)

	for _, want := range []string{
		"declares `rollback=<undo-id>` in its fence tag",
		"companion `{id=<undo-id>}` block",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md missing the forward-declaration rollback phrasing %q", want)
		}
	}

	matches := skillRollbackValueRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		t.Fatal("SKILL.md has no rollback= occurrences at all")
	}
	for _, m := range matches {
		v := m[1]
		if v != "<undo-id>" && !strings.HasPrefix(v, "undo") {
			t.Errorf("SKILL.md contains rollback=%s — rollback= must name the UNDO block (an <undo-id>/undo-* id), never a forward step", v)
		}
	}
}
