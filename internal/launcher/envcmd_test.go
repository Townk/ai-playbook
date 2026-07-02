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
