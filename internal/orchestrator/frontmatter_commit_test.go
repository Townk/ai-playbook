package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/cache"

	"gopkg.in/yaml.v3"
)

// parsedFM mirrors frontmatter.FrontMatter for unmarshaling the saved/cached front
// matter back in tests (we assert on it rather than re-importing the assembler).
type parsedFM struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	Tags        []string
	Env         map[string]struct {
		Value string `yaml:"value"`
		Why   string `yaml:"why"`
	}
	Created     string `yaml:"created"`
	ProjectRoot string `yaml:"project_root"`
	Request     string `yaml:"request"`
}

// splitFM splits a "---\n<yaml>---\n\n<body>" artifact into the parsed front matter
// and the trailing body. It strips exactly the FIRST ---…--- block.
func splitFM(t *testing.T, content string) (parsedFM, string) {
	t.Helper()
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("content has no leading front matter:\n%s", content)
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("no closing front-matter fence:\n%s", content)
	}
	yamlPart := rest[:end+1]
	body := rest[end+len("\n---\n"):]
	// Prepend separates the FM and body with a single blank line; drop it so the
	// body proper can be compared against the original.
	body = strings.TrimPrefix(body, "\n")
	var fm parsedFM
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		t.Fatalf("unmarshal front matter: %v\n%s", err, yamlPart)
	}
	return fm, body
}

// CommitPlaybook assembles + persists the §C/§E front matter: the saved file AND the
// cache entry begin with a ---…--- block carrying the name, the model
// description/category/tags, and an env map with the referenced var's value (+ why
// for the model-noted one); the body follows.
func TestCommitPlaybook_AssemblesFrontMatter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	c := cache.Open()

	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:         sampleReq(),
		Cache:       c,
		CtxHash:     "ctxhash",
		ReqHash:     "reqhash",
		RequestJSON: `{"command":"make build"}`,
		DataRoot:    root,
		Metadata: func(string) (PlaybookMeta, error) {
			return PlaybookMeta{
				Description: "Build an Android app",
				Category:    "Android / build",
				Tags:        []string{"android", "gradle"},
				EnvNotes:    map[string]string{"ANDROID_HOME": "SDK location the build resolves"},
			}, nil
		},
		EnvLookup: func(name string) (string, bool) {
			m := map[string]string{"ANDROID_HOME": "/Users/me/Library/Android/sdk"}
			v, ok := m[name]
			return v, ok
		},
	})

	body := "# Playbook — X\n\nRun `echo $ANDROID_HOME` to confirm.\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm, gotBody := splitFM(t, string(saved))

	if fm.Name != "X" {
		t.Errorf("name = %q, want X", fm.Name)
	}
	if fm.Description != "Build an Android app" {
		t.Errorf("description = %q", fm.Description)
	}
	if fm.Category != "Android / build" {
		t.Errorf("category = %q", fm.Category)
	}
	if strings.Join(fm.Tags, ",") != "android,gradle" {
		t.Errorf("tags = %v", fm.Tags)
	}
	if fm.ProjectRoot != "/home/me/proj" {
		t.Errorf("project_root = %q", fm.ProjectRoot)
	}
	if fm.Request != "fix my build" {
		t.Errorf("request = %q", fm.Request)
	}
	if fm.Created == "" {
		t.Error("created should be set")
	}
	ah, ok := fm.Env["ANDROID_HOME"]
	if !ok {
		t.Fatalf("env missing ANDROID_HOME: %+v", fm.Env)
	}
	if ah.Value != "/Users/me/Library/Android/sdk" {
		t.Errorf("ANDROID_HOME value = %q", ah.Value)
	}
	if ah.Why != "SDK location the build resolves" {
		t.Errorf("ANDROID_HOME why = %q", ah.Why)
	}
	if gotBody != body {
		t.Errorf("body after FM = %q, want %q", gotBody, body)
	}

	// The cache entry also leads with the playbook FM (under the cache's own FM).
	entry, err := os.ReadFile(filepath.Join(root, "cache", "ctxhash", "reqhash.md"))
	if err != nil {
		t.Fatal(err)
	}
	inner := cache.Body(string(entry)) // strip the OUTER cache FM
	cfm, cbody := splitFM(t, inner)
	if cfm.Name != "X" {
		t.Errorf("cache inner FM name = %q, want X", cfm.Name)
	}
	if _, ok := cfm.Env["ANDROID_HOME"]; !ok {
		t.Errorf("cache inner FM missing env: %+v", cfm.Env)
	}
	if cbody != body {
		t.Errorf("cache inner body = %q, want %q", cbody, body)
	}
}

// CommitPlaybook never fails the commit over metadata: a Metadata seam that ERRORS
// (and likewise a nil seam) still persists with name/env/provenance present and the
// model fields empty.
func TestCommitPlaybook_MetadataErrorStillPersists(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta func(string) (PlaybookMeta, error)
	}{
		{"errors", func(string) (PlaybookMeta, error) { return PlaybookMeta{}, errors.New("classify boom") }},
		{"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
				Req:      sampleReq(),
				DataRoot: root,
				Metadata: tc.meta,
				EnvLookup: func(name string) (string, bool) {
					if name == "ANDROID_HOME" {
						return "/sdk", true
					}
					return "", false
				},
			})
			body := "# Playbook — X\n\nUse $ANDROID_HOME.\n"
			path, err := o.CommitPlaybook(body)
			if err != nil {
				t.Fatalf("CommitPlaybook should never fail over metadata: %v", err)
			}
			saved, _ := os.ReadFile(path)
			fm, _ := splitFM(t, string(saved))
			if fm.Name != "X" {
				t.Errorf("name = %q, want X", fm.Name)
			}
			if fm.Description != "" || fm.Category != "" || len(fm.Tags) != 0 {
				t.Errorf("model fields should be empty: %+v", fm)
			}
			if _, ok := fm.Env["ANDROID_HOME"]; !ok {
				t.Errorf("env should still be captured: %+v", fm.Env)
			}
			if fm.Created == "" || fm.ProjectRoot == "" {
				t.Errorf("provenance should be present: %+v", fm)
			}
		})
	}
}

// A secret-named ref has its value redacted in the front matter (name-pattern match).
func TestCommitPlaybook_RedactsSecretValue(t *testing.T) {
	root := t.TempDir()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      sampleReq(),
		DataRoot: root,
		EnvLookup: func(name string) (string, bool) {
			if name == "API_KEY" {
				return "super-secret-token-value", true
			}
			return "", false
		},
	})
	body := "# Playbook — X\n\nexport API_KEY=$API_KEY\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	fm, _ := splitFM(t, string(saved))
	v, ok := fm.Env["API_KEY"]
	if !ok {
		t.Fatalf("env missing API_KEY: %+v", fm.Env)
	}
	if v.Value != "<redacted>" {
		t.Errorf("API_KEY value = %q, want <redacted>", v.Value)
	}
	if strings.Contains(string(saved), "super-secret-token-value") {
		t.Errorf("secret value leaked into the front matter:\n%s", saved)
	}
}

// Store→cache.Body round-trip preserves the inner playbook FM (the §F two-layer
// check): cache.Body strips ONLY the outer cache FM, leaving playbook-FM + body.
func TestCommitPlaybook_CacheBodyPreservesInnerFM(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	c := cache.Open()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      sampleReq(),
		Cache:    c,
		CtxHash:  "ctxhash",
		ReqHash:  "reqhash",
		DataRoot: root,
	})
	body := "# Playbook — Round Trip\n\nbody line.\n"
	if _, err := o.CommitPlaybook(body); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(filepath.Join(root, "cache", "ctxhash", "reqhash.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Two layers: cache FM, then playbook FM, then body.
	if strings.Count(string(entry), "\n---\n") < 2 {
		t.Errorf("expected two front-matter layers in the cache entry:\n%s", entry)
	}
	inner := cache.Body(string(entry))
	if !strings.HasPrefix(inner, "---\n") {
		t.Errorf("cache.Body should preserve the inner playbook FM, got:\n%s", inner)
	}
	fm, gotBody := splitFM(t, inner)
	if fm.Name != "Round Trip" {
		t.Errorf("inner FM name = %q, want Round Trip", fm.Name)
	}
	if gotBody != body {
		t.Errorf("round-trip body = %q, want %q", gotBody, body)
	}
}
