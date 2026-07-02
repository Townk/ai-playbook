package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// writePB writes a valid playbook file (front matter + a minimal body) into dir
// under <stem>.md and returns its path.
func writePB(t *testing.T, dir, stem string, fm frontmatter.FrontMatter) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "# " + fm.Name + "\n\n```bash {id=x}\necho hi\n```\n"
	content := frontmatter.Assemble(fm) + "\n" + body
	path := filepath.Join(dir, stem+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// seamTo points the package seams at the given global + project dirs for the
// duration of a test.
func seamTo(t *testing.T, globalDir, projDir string) {
	t.Helper()
	oldCfg, oldRoot := cfg, projectRoot
	cfg = func() (*config.Config, error) {
		c := config.Default()
		c.Store.Global = globalDir
		c.Store.Project = projDir
		return c, nil
	}
	projectRoot = func() string { return projDir }
	t.Cleanup(func() { cfg, projectRoot = oldCfg, oldRoot })
}

func TestIndexNewestFirstAndProjPrefix(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "alpha", frontmatter.FrontMatter{
		Name: "Alpha", Description: "the alpha", Category: "git",
		Tags: []string{"one"}, Created: "2026-01-01",
	})
	writePB(t, projDir, "beta", frontmatter.FrontMatter{
		Name: "Beta", Description: "the beta", Category: "build",
		Tags: []string{"two"}, Created: "2026-02-01",
	})

	metas, err := Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("want 2 metas, got %d: %+v", len(metas), metas)
	}
	// Newest first: beta (2026-02) before alpha (2026-01).
	if metas[0].Slug != "proj:beta" {
		t.Errorf("metas[0].Slug = %q, want proj:beta", metas[0].Slug)
	}
	if !metas[0].Project {
		t.Errorf("metas[0].Project = false, want true")
	}
	if metas[1].Slug != "alpha" {
		t.Errorf("metas[1].Slug = %q, want alpha", metas[1].Slug)
	}
	if metas[1].Project {
		t.Errorf("metas[1].Project = true, want false")
	}
}

func TestEnvMapped(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "withenv", frontmatter.FrontMatter{
		Name: "WithEnv",
		Env: map[string]frontmatter.EnvValue{
			"FOO": {Value: "bar", Why: "needed"},
		},
		Created: "2026-03-01",
	})

	metas, err := Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1, got %d", len(metas))
	}
	m := metas[0]
	if len(m.Env) != 1 || m.Env[0].Name != "FOO" || m.Env[0].Value != "bar" || m.Env[0].Why != "needed" {
		t.Errorf("Env mapped wrong: %+v", m.Env)
	}
}

func TestLoadProjRoundTrip(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "alpha", frontmatter.FrontMatter{Name: "Alpha", Created: "2026-01-01"})
	writePB(t, projDir, "beta", frontmatter.FrontMatter{Name: "Beta", Created: "2026-02-01"})

	// proj: slug resolves in the project dir.
	m, body, err := Load("proj:beta")
	if err != nil {
		t.Fatalf("Load(proj:beta): %v", err)
	}
	if !m.Project || m.Slug != "proj:beta" {
		t.Errorf("meta = %+v, want proj:beta Project=true", m)
	}
	if !strings.Contains(body, "# Beta") {
		t.Errorf("body missing # Beta: %q", body)
	}

	// bare slug resolves in the global dir.
	if _, gbody, err := Load("alpha"); err != nil || !strings.Contains(gbody, "# Alpha") {
		t.Errorf("Load(alpha) = %q, %v", gbody, err)
	}

	// No shadowing: a proj slug must not find a global file of the same stem.
	if _, _, err := Load("proj:alpha"); err == nil {
		t.Errorf("Load(proj:alpha) should fail (alpha is global-only)")
	}
	// And a bare slug must not find a project file.
	if _, _, err := Load("beta"); err == nil {
		t.Errorf("Load(beta) should fail (beta is project-only)")
	}
}

func TestSearchSubstringCaseInsensitive(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "alpha", frontmatter.FrontMatter{
		Name: "Deploy Helper", Description: "ship it", Category: "ops",
		Tags: []string{"kubernetes"}, Created: "2026-01-01",
	})
	writePB(t, projDir, "beta", frontmatter.FrontMatter{
		Name: "Build", Description: "compile", Category: "build",
		Tags: []string{"go"}, Created: "2026-02-01",
	})

	// name match, case-insensitive
	if got := mustSearch(t, "deploy"); len(got) != 1 || got[0].Slug != "alpha" {
		t.Errorf("search deploy = %+v", got)
	}
	// category match
	if got := mustSearch(t, "BUILD"); len(got) != 1 || got[0].Slug != "proj:beta" {
		t.Errorf("search BUILD = %+v", got)
	}
	// tag match
	if got := mustSearch(t, "kubernetes"); len(got) != 1 || got[0].Slug != "alpha" {
		t.Errorf("search kubernetes = %+v", got)
	}
	// description match
	if got := mustSearch(t, "compile"); len(got) != 1 || got[0].Slug != "proj:beta" {
		t.Errorf("search compile = %+v", got)
	}
}

func mustSearch(t *testing.T, q string) []Meta {
	t.Helper()
	got, err := Search(q)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return got
}

func TestIndexSkipsMalformed(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "good", frontmatter.FrontMatter{Name: "Good", Created: "2026-01-01"})
	// Malformed: opening fence but broken YAML.
	bad := "---\nname: [unterminated\n---\n\n# Bad\n"
	if err := os.WriteFile(filepath.Join(globalDir, "bad.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	metas, err := Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 1 || metas[0].Slug != "good" {
		t.Fatalf("want only good, got %+v", metas)
	}
}

func TestMissingProjectDirIsEmpty(t *testing.T) {
	globalDir := t.TempDir()
	projDir := filepath.Join(t.TempDir(), "does-not-exist")
	seamTo(t, globalDir, projDir)

	writePB(t, globalDir, "alpha", frontmatter.FrontMatter{Name: "Alpha", Created: "2026-01-01"})

	metas, err := Index()
	if err != nil {
		t.Fatalf("Index with missing project dir: %v", err)
	}
	if len(metas) != 1 || metas[0].Slug != "alpha" {
		t.Fatalf("want only global alpha, got %+v", metas)
	}
}

func TestPathForUnknownAndKnown(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	want := writePB(t, globalDir, "alpha", frontmatter.FrontMatter{Name: "Alpha", Created: "2026-01-01"})

	if _, ok := PathFor("nope"); ok {
		t.Errorf("PathFor(nope) ok=true, want false")
	}
	got, ok := PathFor("alpha")
	if !ok || got != want {
		t.Errorf("PathFor(alpha) = %q,%v want %q,true", got, ok, want)
	}
}

func TestCreatedFallsBackToModTime(t *testing.T) {
	globalDir := t.TempDir()
	projDir := t.TempDir()
	seamTo(t, globalDir, projDir)

	// No Created field → fall back to ModTime (must not be zero).
	writePB(t, globalDir, "nodate", frontmatter.FrontMatter{Name: "NoDate"})

	metas, err := Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1, got %d", len(metas))
	}
	if metas[0].Created.IsZero() {
		t.Errorf("Created is zero, want ModTime fallback")
	}
}

// TestMetaFromFM_CopiesDependsOn verifies metaFromFM copies DependsOn from the
// parsed front matter onto Meta.
func TestMetaFromFM_CopiesDependsOn(t *testing.T) {
	fm := frontmatter.FrontMatter{Name: "X", DependsOn: []string{"x"}}
	m := metaFromFM(fm, filepath.Join(t.TempDir(), "x.md"), false)
	if !reflect.DeepEqual(m.DependsOn, []string{"x"}) {
		t.Fatalf("Meta.DependsOn = %v, want [x]", m.DependsOn)
	}
}

func TestLoad_ReadsProjectBound(t *testing.T) {
	globalDir := t.TempDir()
	seamTo(t, globalDir, t.TempDir())
	writePB(t, globalDir, "pb", frontmatter.FrontMatter{Name: "PB", ProjectBound: true})
	m, _, err := Load("pb")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !m.ProjectBound {
		t.Fatal("Meta.ProjectBound must be read from front matter")
	}
}
