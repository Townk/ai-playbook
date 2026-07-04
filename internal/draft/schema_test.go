package draft

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMeta_EnvRoundTrip(t *testing.T) {
	in := Playbook{Title: "T", Meta: Meta{Env: []EnvVar{{Name: "ANDROID_SDK_ROOT", Why: "the SDK"}}}}
	b, _ := json.Marshal(in)
	if !strings.Contains(string(b), `"name":"ANDROID_SDK_ROOT"`) || !strings.Contains(string(b), `"why":"the SDK"`) {
		t.Fatalf("env did not serialize: %s", b)
	}
	var out Playbook
	if err := json.Unmarshal(b, &out); err != nil || len(out.Meta.Env) != 1 || out.Meta.Env[0].Name != "ANDROID_SDK_ROOT" {
		t.Fatalf("env round-trip failed: %+v err=%v", out.Meta.Env, err)
	}
}

func TestPlaybook_JSONRoundTrip(t *testing.T) {
	in := Playbook{
		Title: "Fix the wrapper",
		Intro: "lead prose",
		Sections: []Section{{
			Heading: "Goal & error",
			Content: []ContentItem{
				{Kind: "text", Text: "what happened"},
				{Kind: "code", Lang: "console", Code: "boom", Static: true},
				{Kind: "code", Lang: "bash", Code: "echo fix", ID: "fix", Needs: []string{"diag"}},
			},
		}},
		Verify: &Step{Lang: "bash", Code: "echo ok", Needs: []string{"fix"}},
		Meta:   Meta{Description: "d", Category: "c", Tags: []string{"t"}, ProjectBound: true},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Playbook
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Title != "Fix the wrapper" || len(out.Sections) != 1 || len(out.Sections[0].Content) != 3 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Verify == nil || out.Verify.Needs[0] != "fix" {
		t.Fatalf("verify lost: %+v", out.Verify)
	}
	if !out.Meta.ProjectBound {
		t.Fatalf("project_bound lost")
	}
	// project_bound must serialize as the snake_case key the front matter uses.
	if !strings.Contains(string(b), `"project_bound":true`) {
		t.Fatalf("project_bound json key wrong: %s", b)
	}
}
