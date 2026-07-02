package launcher

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

func TestResolveEnvJSON(t *testing.T) {
	vars := map[string]frontmatter.EnvValue{
		"PLAIN":      {Value: "default-plain"}, // no env → default
		"FROM_ENV":   {Value: "default"},       // env set → env wins
		"API_KEY":    {Value: "<redacted>"},    // secret name → "" even if exported
		"MASKED_DEF": {Value: "<redacted>"},    // build-time masked, env unset → ""
		"HIENTROPY":  {Value: "safe-default"},  // non-secret name, high-entropy env value → ""
	}
	getenv := func(name string) string {
		switch name {
		case "FROM_ENV":
			return "live-value"
		case "API_KEY":
			return "sk-supersecrettoken1234567890"
		case "HIENTROPY":
			return "Xk9$2mQ7!pL4wZ8#vR1nB6" // mixed-charset high-entropy → looksLikeSecret
		}
		return ""
	}
	out, redacted := resolveEnvJSON(vars, getenv)
	if out["PLAIN"] != "default-plain" {
		t.Errorf("PLAIN = %q, want default", out["PLAIN"])
	}
	if out["FROM_ENV"] != "live-value" {
		t.Errorf("FROM_ENV = %q, want live env value", out["FROM_ENV"])
	}
	for _, k := range []string{"API_KEY", "MASKED_DEF", "HIENTROPY"} {
		if out[k] != "" {
			t.Errorf("%s must be redacted to \"\", got %q", k, out[k])
		}
	}
	// redacted names sorted, and exactly the three sensitive ones
	want := []string{"API_KEY", "HIENTROPY", "MASKED_DEF"}
	if !reflect.DeepEqual(redacted, want) {
		t.Errorf("redacted = %v, want %v", redacted, want)
	}
}

// TestResolveEnvJSON_BuiltMaskStaysRedacted covers the edge the declared-default
// gate closes: a var whose default was masked at build time but whose name is NOT
// secret-looking and whose live override is short/low-entropy (so frontmatter.Redact
// alone would miss it) must STILL come out "" — once masked at build, always masked.
func TestResolveEnvJSON_BuiltMaskStaysRedacted(t *testing.T) {
	vars := map[string]frontmatter.EnvValue{
		"BLOB": {Value: "<redacted>"}, // masked at build (by value entropy), benign name
	}
	getenv := func(name string) string {
		if name == "BLOB" {
			return "ab" // short, low-entropy: Redact's heuristic would not flag it
		}
		return ""
	}
	out, redacted := resolveEnvJSON(vars, getenv)
	if out["BLOB"] != "" {
		t.Errorf("a build-masked var must stay redacted despite a benign override; got %q", out["BLOB"])
	}
	if !reflect.DeepEqual(redacted, []string{"BLOB"}) {
		t.Errorf("redacted = %v, want [BLOB]", redacted)
	}
}

func TestResolveEnvArgs(t *testing.T) {
	if ra, err := resolveEnvArgs([]string{"--file", "p.md"}); err != nil || ra.Kind != "file" || ra.Value != "p.md" {
		t.Fatalf("--file: ra=%+v err=%v", ra, err)
	}
	if ra, err := resolveEnvArgs([]string{"my-slug"}); err != nil || ra.Kind != "playbook" || ra.Value != "my-slug" {
		t.Fatalf("bare: ra=%+v err=%v", ra, err)
	}
	if _, err := resolveEnvArgs([]string{}); err == nil {
		t.Error("zero sources must error")
	}
	if _, err := resolveEnvArgs([]string{"slug", "--file", "p.md"}); err == nil {
		t.Error("two sources must error")
	}
}

func TestEnvMain_FileSmoke(t *testing.T) {
	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\nenv:\n  FOO:\n    value: bar\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "envpb.md", pb) // reuse the launcher temp-file helper
	withArgs(t, []string{"ai-playbook", "env", "--file", path})
	out := captureStdout(t, func() { // reuse the package's os.Pipe capture helper
		if code := EnvMain(); code != 0 {
			t.Fatalf("EnvMain exit %d, want 0", code)
		}
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", got["FOO"])
	}
}

// ---- EnvMain: depends_on chain union ----

// TestEnvMain_ChainUnion verifies a parent declaring depends_on emits the
// UNION of its own declared vars and every dependency's — both A (the
// parent's) and B (the dependency's) must appear in the output.
func TestEnvMain_ChainUnion(t *testing.T) {
	depPath := writeDepPlaybook(t, t.TempDir(), "dep", "env:\n  B:\n    value: depval\n")
	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "dep" {
			return depPath, true
		}
		return "", false
	})()

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - dep\nenv:\n  A:\n    value: parentval\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "parent.md", pb)
	withArgs(t, []string{"ai-playbook", "env", "--file", path})
	out := captureStdout(t, func() {
		if code := EnvMain(); code != 0 {
			t.Fatalf("EnvMain exit %d, want 0", code)
		}
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if got["A"] != "parentval" {
		t.Errorf("A = %q, want parentval (parent's own declared var)", got["A"])
	}
	if got["B"] != "depval" {
		t.Errorf("B = %q, want depval (dependency's declared var)", got["B"])
	}
}

// TestEnvMain_ChainCollision_ParentWins verifies that when the parent and a
// dependency both declare the same var name, the parent's value wins.
func TestEnvMain_ChainCollision_ParentWins(t *testing.T) {
	depPath := writeDepPlaybook(t, t.TempDir(), "dep", "env:\n  X:\n    value: d\n")
	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "dep" {
			return depPath, true
		}
		return "", false
	})()

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - dep\nenv:\n  X:\n    value: p\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "parent.md", pb)
	withArgs(t, []string{"ai-playbook", "env", "--file", path})
	out := captureStdout(t, func() {
		if code := EnvMain(); code != 0 {
			t.Fatalf("EnvMain exit %d, want 0", code)
		}
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if got["X"] != "p" {
		t.Errorf("X = %q, want %q (parent wins the collision)", got["X"], "p")
	}
}

// TestEnvMain_ChainIssues_Exit2 verifies a dangling/cyclic depends_on chain
// exits 2 (mirroring the run gate), never printing a JSON body.
func TestEnvMain_ChainIssues_Exit2(t *testing.T) {
	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - ghost\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "parent.md", pb)
	withArgs(t, []string{"ai-playbook", "env", "--file", path})
	if code := EnvMain(); code != 2 {
		t.Fatalf("dangling depends_on → exit %d, want 2", code)
	}
}
