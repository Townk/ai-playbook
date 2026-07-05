package author

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
)

// recallGeneralReq is the representative non-failure request behind the recall
// characterization goldens (kept in lockstep with the generator that snapshotted
// testdata/recall/systemprompt_general.golden from the PRE-recall builders).
func recallGeneralReq() capture.Request {
	return capture.Request{
		Kind:        "question",
		Command:     "git status",
		Exit:        "0",
		UserRequest: "how do I add a git remote",
		ProjectRoot: "/home/me/proj",
		CWD:         "/home/me/proj",
		Project:     capture.Project{Name: "proj", Branch: "main"},
	}
}

// readGolden loads a captured pre-recall snapshot.
func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "recall", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(b)
}

// TestRecall_EmptyKBByteIdentical is the characterization heart of K3: with BOTH
// knowledge sets empty, every one of the SIX prompt builders (the four gaining
// recall + the two that must stay lean) must reproduce, byte-for-byte, the output
// captured from the PRE-recall builders. The goldens were snapshotted before the
// signatures changed; empty-KB parity proves the fold is inert when there is
// nothing to recall.
func TestRecall_EmptyKBByteIdentical(t *testing.T) {
	fail := sampleFailure()
	gen := recallGeneralReq()

	dsys, duser := DriftRegenPrompt("current file content", "stale patch content", "", "")

	cases := []struct {
		golden string
		got    string
	}{
		{"systemprompt_failure.golden", SystemPrompt(fail, "", "", "zsh")},
		{"systemprompt_general.golden", SystemPrompt(gen, "", "", "zsh")},
		{"followup.golden", FollowupPrompt(fail, "boom: still broken", "", "")},
		{"finalplaybook_fresh.golden", FinalPlaybookPrompt(fail, "", "the resolved troubleshoot", "", "")},
		{"finalplaybook_amend.golden", FinalPlaybookPrompt(fail, "# Playbook — base\n\n```bash {id=verify}\ntrue\n```\n", "fold this in", "", "")},
		{"driftregen_sys.golden", dsys},
		{"driftregen_user.golden", duser},
		{"classify.golden", ClassifyPrompt(sampleClassifyRequest())},
		{"metadata.golden", MetadataPrompt("# Playbook — x\n\n```bash {id=verify}\ntrue\n```\n")},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			if want := readGolden(t, tc.golden); tc.got != want {
				t.Errorf("empty-KB output drifted from the pre-recall golden %s\n--- got ---\n%s\n--- want ---\n%s", tc.golden, tc.got, want)
			}
		})
	}
}

const recallGlobal KnowledgeBase = "## System\n- macOS on Apple Silicon\n\n## User\n- prefers concise answers\n"
const recallProject KnowledgeBase = "## Environment\n- deploys via fly.io\n\n## Topics\n### builds\n- uses bazel, not make\n"

// assertTwoSetFold checks that a rendered prompt carries the two-part recall block
// in the contract shape: the parent heading, both subheadings, global content
// BEFORE project content, and every fact present.
func assertTwoSetFold(t *testing.T, sys string) {
	t.Helper()
	iHead := strings.Index(sys, "## What we already know about this project")
	iGlobalSub := strings.Index(sys, "### About this machine and user")
	iProjectSub := strings.Index(sys, "### About this project")
	if iHead < 0 || iGlobalSub < 0 || iProjectSub < 0 {
		t.Fatalf("missing recall heading/subheadings\n%s", sys)
	}
	// Order: parent heading, then global sub, then project sub (global first).
	if iHead >= iGlobalSub || iGlobalSub >= iProjectSub {
		t.Errorf("fold order wrong: head=%d global=%d project=%d\n%s", iHead, iGlobalSub, iProjectSub, sys)
	}
	for _, fact := range []string{"macOS on Apple Silicon", "prefers concise answers", "deploys via fly.io", "uses bazel, not make"} {
		if !strings.Contains(sys, fact) {
			t.Errorf("recall fact %q missing\n%s", fact, sys)
		}
	}
	// Global set text must precede project set text.
	if strings.Index(sys, "macOS on Apple Silicon") > strings.Index(sys, "deploys via fly.io") {
		t.Errorf("global set must appear before project set\n%s", sys)
	}
}

func TestRecall_SystemPromptFoldsBothSets(t *testing.T) {
	assertTwoSetFold(t, SystemPrompt(sampleFailure(), recallGlobal, recallProject, "zsh"))
}

func TestRecall_FollowupFoldsBothSets(t *testing.T) {
	assertTwoSetFold(t, FollowupPrompt(sampleFailure(), "boom", recallGlobal, recallProject))
}

func TestRecall_FinalPlaybookFoldsBothSets(t *testing.T) {
	assertTwoSetFold(t, FinalPlaybookPrompt(sampleFailure(), "", "resolved", recallGlobal, recallProject))
	// Amend mode too.
	assertTwoSetFold(t, FinalPlaybookPrompt(sampleFailure(), "# Playbook — base\n", "change", recallGlobal, recallProject))
}

func TestRecall_DriftRegenFoldsBothSets(t *testing.T) {
	sys, _ := DriftRegenPrompt("cur", "stale", recallGlobal, recallProject)
	assertTwoSetFold(t, sys)
}

// TestRecall_SingleSetOmitsOtherSubheading: a present set emits its subheading;
// an empty set omits its subheading entirely (no empty "### About …" left over).
func TestRecall_SingleSetOmitsOtherSubheading(t *testing.T) {
	globalOnly := SystemPrompt(sampleFailure(), recallGlobal, "", "zsh")
	if !strings.Contains(globalOnly, "### About this machine and user") {
		t.Errorf("global-only fold missing its subheading")
	}
	if strings.Contains(globalOnly, "### About this project") {
		t.Errorf("global-only fold must not emit the project subheading")
	}
	projectOnly := SystemPrompt(sampleFailure(), "", recallProject, "zsh")
	if !strings.Contains(projectOnly, "### About this project") {
		t.Errorf("project-only fold missing its subheading")
	}
	if strings.Contains(projectOnly, "### About this machine and user") {
		t.Errorf("project-only fold must not emit the global subheading")
	}
}

// TestRecall_ClassifyAndMetadataStayLean pins NEGATIVELY: the two lean structured
// calls never carry recall, regardless of what is on disk (their signatures can't
// even take a KB — this guards against a future accidental fold).
func TestRecall_ClassifyAndMetadataStayLean(t *testing.T) {
	classify := ClassifyPrompt(sampleClassifyRequest())
	metadata := MetadataPrompt("# Playbook — x\n\n```bash {id=verify}\ntrue\n```\n")
	for _, marker := range []string{
		"## What we already know about this project",
		"### About this machine and user",
		"### About this project",
	} {
		if strings.Contains(classify, marker) {
			t.Errorf("ClassifyPrompt must stay lean but carried recall marker %q", marker)
		}
		if strings.Contains(metadata, marker) {
			t.Errorf("MetadataPrompt must stay lean but carried recall marker %q", marker)
		}
	}
}
