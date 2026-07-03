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

// Finding A7c: the braced alternative must also match parameter-expansion
// forms (${VAR:-default}, ${VAR%.*}, ${VAR#prefix}, ${VAR/a/b}), not just the
// bare ${VAR} form — otherwise those vars are silently omitted from env:.
func TestScanEnvRefs_ParameterExpansionForms(t *testing.T) {
	body := "```sh\n" +
		"echo ${FOO:-bar}\n" +
		"echo ${BAZ%.*}\n" +
		"```\n"
	got := ScanEnvRefs(body)
	want := []string{"BAZ", "FOO"}
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

func TestIsRedactedMask(t *testing.T) {
	if !IsRedactedMask("<redacted>") {
		t.Error("the mask must be recognized")
	}
	if IsRedactedMask("") || IsRedactedMask("real-value") || IsRedactedMask("<redacted> ") {
		t.Error("only the exact mask string is the mask")
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

	got := BuildEnv(refs, notes, lookup, "")

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
	got := BuildEnv([]string{"ANDROID_HOME"}, nil, lookup, "")
	if got["ANDROID_HOME"].Why != "" {
		t.Fatalf("scan-only var should have empty Why, got %q", got["ANDROID_HOME"].Why)
	}
}

func TestBuildEnv_EmptyIsNil(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	if got := BuildEnv([]string{"NOPE"}, nil, lookup, ""); got != nil {
		t.Fatalf("BuildEnv with no resolvable vars = %v, want nil", got)
	}
}

// TestBuildEnv_SkipsNonPortable verifies the denylist: referenced/noted
// non-portable vars (HOME, PATH) are SKIPPED even when present in the env, while
// portable vars ($ANDROID_HOME/$JAVA_HOME) are still captured.
func TestBuildEnv_SkipsNonPortable(t *testing.T) {
	env := map[string]string{
		"HOME":         "/Users/thiago",
		"PATH":         "/usr/bin:/bin",
		"ANDROID_HOME": "/opt/android",
		"JAVA_HOME":    "/opt/jdk17",
	}
	lookup := func(n string) (string, bool) { v, ok := env[n]; return v, ok }

	// HOME referenced in the body; PATH flagged in notes; both must be dropped.
	refs := []string{"HOME", "ANDROID_HOME", "JAVA_HOME"}
	notes := map[string]string{"PATH": "the search path", "JAVA_HOME": "the JDK"}

	got := BuildEnv(refs, notes, lookup, "")

	if _, ok := got["HOME"]; ok {
		t.Errorf("HOME must be skipped (non-portable), got %+v", got["HOME"])
	}
	if _, ok := got["PATH"]; ok {
		t.Errorf("PATH must be skipped even when flagged in notes, got %+v", got["PATH"])
	}
	want := map[string]EnvValue{
		"ANDROID_HOME": {Value: "/opt/android"},
		"JAVA_HOME":    {Value: "/opt/jdk17", Why: "the JDK"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildEnv = %#v, want %#v", got, want)
	}
}

// TestBuildEnv_NormalizesHomeValues verifies captured VALUES get a leading
// home-dir prefix rewritten to "~"; an exact-home value becomes "~"; a non-home
// value is untouched; an empty home disables normalization.
func TestBuildEnv_NormalizesHomeValues(t *testing.T) {
	const home = "/Users/thiago"
	env := map[string]string{
		"JAVA_HOME":    home + "/.local/share/mise/x",
		"ANDROID_HOME": "/opt/android",
		"GEM_HOME":     home,
	}
	lookup := func(n string) (string, bool) { v, ok := env[n]; return v, ok }
	refs := []string{"JAVA_HOME", "ANDROID_HOME", "GEM_HOME"}

	got := BuildEnv(refs, nil, lookup, home)
	want := map[string]EnvValue{
		"JAVA_HOME":    {Value: "~/.local/share/mise/x"},
		"ANDROID_HOME": {Value: "/opt/android"},
		"GEM_HOME":     {Value: "~"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildEnv (home=%q) = %#v, want %#v", home, got, want)
	}

	// Empty home: values are left exactly as the lookup returns them.
	gotRaw := BuildEnv(refs, nil, lookup, "")
	if gotRaw["JAVA_HOME"].Value != home+"/.local/share/mise/x" {
		t.Errorf("empty home must disable normalization, got %q", gotRaw["JAVA_HOME"].Value)
	}
	if gotRaw["GEM_HOME"].Value != home {
		t.Errorf("empty home must leave exact-home value, got %q", gotRaw["GEM_HOME"].Value)
	}
}

// TestNormalizeHome covers equal, prefix, non-match, empty home, and a value that
// merely CONTAINS home mid-string (which must NOT be rewritten).
func TestNormalizeHome(t *testing.T) {
	const home = "/Users/thiago"
	cases := []struct {
		s, home, want string
	}{
		{home, home, "~"}, // exact equal
		{home + "/.config/foo", home, "~/.config/foo"},       // leading prefix
		{"/opt/android", home, "/opt/android"},               // non-match
		{home + "/.config", "", home + "/.config"},           // empty home: unchanged
		{"/data" + home + "/x", home, "/data" + home + "/x"}, // home mid-string: unchanged
		{home + "x/y", home, home + "x/y"},                   // prefix without "/" boundary: unchanged
	}
	for _, c := range cases {
		if got := NormalizeHome(c.s, c.home); got != c.want {
			t.Errorf("NormalizeHome(%q,%q) = %q, want %q", c.s, c.home, got, c.want)
		}
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

// TestParse_StripsAndParses verifies Parse pulls a leading front-matter block off
// the document, parses its scalar/slice/nested fields, and returns the FM-free
// body (with the single Prepend separator blank line consumed).
func TestParse_StripsAndParses(t *testing.T) {
	content := "---\n" +
		"name: Playbook — Android build\n" +
		"description: Compile the app and fix the SDK path\n" +
		"category: Android / build\n" +
		"tags:\n  - android\n  - gradle\n" +
		"env:\n" +
		"  ANDROID_HOME:\n    value: /Users/x/Library/Android/sdk\n    why: SDK location\n" +
		"---\n\n" +
		"# Playbook — Android build\n\nDo the thing.\n"

	fm, body, ok := Parse(content)
	if !ok {
		t.Fatalf("Parse must report ok for a leading front-matter block")
	}
	if fm.Name != "Playbook — Android build" {
		t.Errorf("Name = %q", fm.Name)
	}
	if fm.Description != "Compile the app and fix the SDK path" {
		t.Errorf("Description = %q", fm.Description)
	}
	if fm.Category != "Android / build" {
		t.Errorf("Category = %q", fm.Category)
	}
	if !reflect.DeepEqual(fm.Tags, []string{"android", "gradle"}) {
		t.Errorf("Tags = %v", fm.Tags)
	}
	got := fm.Env["ANDROID_HOME"]
	if got.Value != "/Users/x/Library/Android/sdk" || got.Why != "SDK location" {
		t.Errorf("nested env = %+v", got)
	}
	if body != "# Playbook — Android build\n\nDo the thing.\n" {
		t.Fatalf("body must be FM-free starting at the H1, got %q", body)
	}
}

// TestParse_RoundTripsPrepend verifies Prepend → Parse round-trips: the parsed
// front matter equals the original and the parsed body equals the original body.
func TestParse_RoundTripsPrepend(t *testing.T) {
	fm := FrontMatter{
		Name:        "Playbook — X",
		Description: "one-line desc",
		Category:    "cat",
		Tags:        []string{"a", "b"},
		Env:         map[string]EnvValue{"FOO": {Value: "bar", Why: "because"}},
		Created:     "2026-06-25",
		ProjectRoot: "/proj",
		Request:     "do x",
	}
	body := "# Playbook — X\n\nDo the thing.\n"

	gotFM, gotBody, ok := Parse(Prepend(fm, body))
	if !ok {
		t.Fatalf("round-trip Parse must report ok")
	}
	if !reflect.DeepEqual(gotFM, fm) {
		t.Errorf("round-trip FM mismatch:\n got=%+v\nwant=%+v", gotFM, fm)
	}
	if gotBody != body {
		t.Errorf("round-trip body mismatch:\n got=%q\nwant=%q", gotBody, body)
	}
}

// TestParse_NoFrontMatter verifies content without a leading FM block returns
// ok=false and the content unchanged (old saved files, fresh drafts).
func TestParse_NoFrontMatter(t *testing.T) {
	content := "# Playbook — X\n\nNo front matter here.\n"
	fm, body, ok := Parse(content)
	if ok {
		t.Fatalf("content with no leading FM must report ok=false")
	}
	if body != content {
		t.Fatalf("body must be unchanged, got %q", body)
	}
	if !reflect.DeepEqual(fm, FrontMatter{}) {
		t.Fatalf("FM must be zero, got %+v", fm)
	}
}

// TestParse_IgnoresFenceInsideCodeBlock verifies a "---" appearing inside a
// fenced code block in the body is NOT mistaken for front matter: only a block at
// the very start of the document counts.
func TestParse_IgnoresFenceInsideCodeBlock(t *testing.T) {
	content := "# Playbook — X\n\n```yaml\n---\nfoo: bar\n---\n```\n\nDone.\n"
	_, body, ok := Parse(content)
	if ok {
		t.Fatalf("a --- inside a body code block must not be treated as front matter")
	}
	if body != content {
		t.Fatalf("body must be unchanged when there is no leading FM, got %q", body)
	}
}

// TestParse_UnterminatedIsNotFrontMatter verifies a leading "---\n" with no
// closing fence is not treated as front matter (content returned unchanged).
func TestParse_UnterminatedIsNotFrontMatter(t *testing.T) {
	content := "---\nname: X\nno closing fence here\n"
	_, body, ok := Parse(content)
	if ok {
		t.Fatalf("unterminated front matter must report ok=false")
	}
	if body != content {
		t.Fatalf("body must be unchanged, got %q", body)
	}
}

// TestFrontMatter_DependsOnRoundTrip verifies depends_on round-trips through
// Parse and that Assemble re-emits a depends_on: block.
func TestFrontMatter_DependsOnRoundTrip(t *testing.T) {
	fm := FrontMatter{Name: "N", DependsOn: []string{"a", "b"}}

	assembled := Assemble(fm)
	if !strings.Contains(assembled, "depends_on") {
		t.Fatalf("depends_on not assembled:\n%s", assembled)
	}

	got, _, ok := Parse(Assemble(fm) + "\nbody\n")
	if !ok {
		t.Fatalf("Parse must report ok")
	}
	if !reflect.DeepEqual(got.DependsOn, []string{"a", "b"}) {
		t.Fatalf("DependsOn = %v, want [a b]", got.DependsOn)
	}
}

func TestFrontMatter_ProjectBoundRoundTrip(t *testing.T) {
	fm := FrontMatter{Name: "N", ProjectBound: true}
	full := Prepend(fm, "body")
	if !strings.Contains(full, "project_bound: true") {
		t.Fatalf("project_bound not assembled:\n%s", full)
	}
	got, _, ok := Parse(full)
	if !ok || !got.ProjectBound {
		t.Fatalf("project_bound did not round-trip: ok=%v fm=%+v", ok, got)
	}
}
