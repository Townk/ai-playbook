package frontmatter

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScanEnvRefs(t *testing.T) {
	body := `# Playbook — Android build

Set $ANDROID_HOME and confirm ${JAVA_HOME}.

` + "```sh\n" +
		"export FOO=1\n" +
		"BAR=baz ./gradlew assemble\n" +
		"echo $ANDROID_HOME\n" + // duplicate, must dedup
		"```\n" +
		`This explains HOME and user settings in prose; lowercase $path is ignored.`

	got := ScanEnvRefs(body)
	want := []string{"ANDROID_HOME", "BAR", "FOO", "JAVA_HOME"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanEnvRefs = %v, want %v", got, want)
	}
}

func TestScanEnvRefs_IgnoresProseAndLowercase(t *testing.T) {
	body := "We set HOME, USER and LANG by hand. Use the home directory. $path stays."
	if got := ScanEnvRefs(body); len(got) != 0 {
		t.Fatalf("ScanEnvRefs = %v, want empty (no reference forms)", got)
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		wantOut   string
		wantRedac bool
	}{
		{"GITHUB_TOKEN", "ghp_abc123def456", redactedMask, true},
		{"API_KEY", "anything", redactedMask, true},
		{"DB_PASSWORD", "hunter2", redactedMask, true},
		{"SOME_SECRET", "x", redactedMask, true},
		{"AWS_CREDENTIAL", "y", redactedMask, true},
		// high-entropy value under an innocuous name
		{"OPAQUE", "aB3xY9zQ1mN7pL2kR8wT", redactedMask, true},
		// left intact
		{"ANDROID_HOME", "/Users/thiago/Library/Android/sdk", "/Users/thiago/Library/Android/sdk", false},
		{"GRADLE_OPTS", "-Xmx2g", "-Xmx2g", false},
		{"JAVA_VERSION", "17", "17", false},
		{"DESC", "a normal sentence with spaces and length", "a normal sentence with spaces and length", false},
	}
	for _, c := range cases {
		out, red := Redact(c.name, c.value)
		if out != c.wantOut || red != c.wantRedac {
			t.Errorf("Redact(%q,%q) = (%q,%v), want (%q,%v)",
				c.name, c.value, out, red, c.wantOut, c.wantRedac)
		}
	}
}

func TestBuildEnv(t *testing.T) {
	env := map[string]string{
		"ANDROID_HOME": "/Users/thiago/Library/Android/sdk",
		"JAVA_HOME":    "/Library/Java/jdk17",
		"GITHUB_TOKEN": "ghp_supersecretvalue",
		// GRADLE_OPTS deliberately absent from the env
	}
	lookup := func(n string) (string, bool) { v, ok := env[n]; return v, ok }

	refs := []string{"ANDROID_HOME", "GRADLE_OPTS"} // scan-only (no why)
	notes := map[string]string{
		"ANDROID_HOME": "SDK location the Gradle build resolves against",
		"JAVA_HOME":    "JDK the Gradle toolchain uses",
		"GITHUB_TOKEN": "auth token the publish step uses",
	}

	got := BuildEnv(refs, notes, lookup)

	want := map[string]EnvValue{
		"ANDROID_HOME": {Value: "/Users/thiago/Library/Android/sdk", Why: "SDK location the Gradle build resolves against"},
		"JAVA_HOME":    {Value: "/Library/Java/jdk17", Why: "JDK the Gradle toolchain uses"},
		"GITHUB_TOKEN": {Value: redactedMask, Why: "auth token the publish step uses"},
		// GRADLE_OPTS omitted: absent from env.
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildEnv = %#v, want %#v", got, want)
	}
}

func TestBuildEnv_ScanOnlyHasNoWhy(t *testing.T) {
	env := map[string]string{"ANDROID_HOME": "/sdk"}
	lookup := func(n string) (string, bool) { v, ok := env[n]; return v, ok }
	got := BuildEnv([]string{"ANDROID_HOME"}, nil, lookup)
	if got["ANDROID_HOME"].Why != "" {
		t.Fatalf("scan-only var should have empty Why, got %q", got["ANDROID_HOME"].Why)
	}
}

func TestBuildEnv_EmptyIsNil(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	if got := BuildEnv([]string{"NOPE"}, nil, lookup); got != nil {
		t.Fatalf("BuildEnv with no resolvable vars = %v, want nil", got)
	}
}

func TestAssembleFences(t *testing.T) {
	out := Assemble(FrontMatter{Name: "Playbook — X"})
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("Assemble must start with ---, got %q", out[:min(10, len(out))])
	}
	if !strings.HasSuffix(out, "---\n") {
		t.Errorf("Assemble must end with ---, got tail %q", out[max(0, len(out)-10):])
	}
}

func TestAssembleRoundTrip(t *testing.T) {
	fm := FrontMatter{
		Name:        "Playbook — Fix Gradle: the build",
		Description: "Fix the build: resolve SDK path issues", // colon
		Category:    "Android / build",
		Tags:        []string{"android", "gradle"},
		Env: map[string]EnvValue{
			"ANDROID_HOME": {Value: "/Users/thiago/Library/Android/sdk", Why: "SDK: where it lives"},
			"GRADLE_TOKEN": {Value: redactedMask, Why: "auth token the publish step uses"},
		},
		Created:     "2026-06-25T12:00:00Z",
		ProjectRoot: "/Users/thiago/Projects/app",
		Request:     "build: fails with SDK not found",
	}

	out := Assemble(fm)

	// Strip the fences and round-trip the YAML body.
	inner := strings.TrimSuffix(strings.TrimPrefix(out, "---\n"), "---\n")
	var back FrontMatter
	if err := yaml.Unmarshal([]byte(inner), &back); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, fm) {
		t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", back, fm)
	}

	// Spot-check that the colon-bearing description and the path survived intact.
	if back.Description != "Fix the build: resolve SDK path issues" {
		t.Errorf("description with colon corrupted: %q", back.Description)
	}
	if back.ProjectRoot != "/Users/thiago/Projects/app" {
		t.Errorf("path value corrupted: %q", back.ProjectRoot)
	}
	if back.Env["ANDROID_HOME"].Why != "SDK: where it lives" {
		t.Errorf("nested env why with colon corrupted: %q", back.Env["ANDROID_HOME"].Why)
	}
}

func TestPrepend(t *testing.T) {
	fm := FrontMatter{Name: "Playbook — X"}
	body := "# Playbook — X\n\nDo the thing.\n"
	out := Prepend(fm, body)

	if !strings.HasPrefix(out, "---\n") {
		t.Fatalf("Prepend must start with front matter, got %q", out[:10])
	}
	fmEnd := strings.Index(out, "---\n\n")
	bodyStart := strings.Index(out, "# Playbook")
	if fmEnd == -1 || bodyStart == -1 || bodyStart < fmEnd {
		t.Fatalf("Prepend must put front matter above body; out=%q", out)
	}
	if !strings.HasSuffix(out, body) {
		t.Fatalf("Prepend must end with the body verbatim; out=%q", out)
	}
}
