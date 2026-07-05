// Package kb is the Go port of the shell knowledge-base reader
// (assist::kb_path / assist::kb_ensure in assist-agent-common.zsh): a per-project
// file of distilled facts the producer folds into the system prompt as the
// "## What we already know about this project" section.
//
// The on-disk layout mirrors the shell exactly so a Go reader and the legacy
// shell writer (the deferred `remember` tool) agree on the path:
//
//	$root/projects/<project_key>/knowledge.md
//
// where project_key = SHA-1 (lowercase hex) of the project-root path string and
// $root is the same data dir the cache uses: AI_PLAYBOOK_DATA_DIR, else
// ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook.
//
// Append appends a distilled "- <fact>" line to the per-project knowledge.md
// (the `remember` tool's KB write).
package kb

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/Townk/ai-playbook/internal/cache"
)

// KnowledgeBase is distilled-facts text, verbatim from a KB file. Empty when the
// file is missing or empty.
type KnowledgeBase string

// DefaultRoot resolves the data-dir root shared by the cache and the KB:
// AI_PLAYBOOK_DATA_DIR, else ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook.
// It delegates to cache.DefaultRoot — the one shared resolver — so the two
// packages can never drift.
func DefaultRoot() string {
	return cache.DefaultRoot()
}

// projectKey reproduces assist::project_key: the lowercase hex SHA-1 of the
// project-root path string (no trailing newline — `print -rn`).
func projectKey(projectRoot string) string {
	sum := sha1.Sum([]byte(projectRoot))
	return hex.EncodeToString(sum[:])
}

// Path returns the PROJECT KB file path for projectRoot under the given root,
// mirroring assist::kb_path: $root/projects/<project_key>/knowledge.md.
func Path(root, projectRoot string) string {
	return filepath.Join(root, "projects", projectKey(projectRoot), "knowledge.md")
}

// GlobalPath returns the GLOBAL KB file path under the given root:
// $root/knowledge.md (sections ## System / ## User).
func GlobalPath(root string) string {
	return filepath.Join(root, "knowledge.md")
}

// LoadGlobal reads the global KB file (root/knowledge.md) verbatim. A missing or
// unreadable file → "" (best-effort, never fatal). The global file is always
// sectioned (## System / ## User) and carries no meta line.
func LoadGlobal(root string) KnowledgeBase {
	b, err := os.ReadFile(GlobalPath(root))
	if err != nil {
		return ""
	}
	return KnowledgeBase(b)
}

// LoadProject reads the per-project KB for projectRoot verbatim, performing the
// LAZY MIGRATION READ: a legacy (unsectioned) file is presented as if its
// bullets lived under a ## Environment section, so recall sees the taxonomy even
// before the first sectioned write rewrites the file on disk. An already
// sectioned file is returned verbatim. Missing/empty/unreadable → "".
func LoadProject(root, projectRoot string) KnowledgeBase {
	b, err := os.ReadFile(Path(root, projectRoot))
	if err != nil {
		return ""
	}
	content := string(b)
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if isSectioned(content) {
		return KnowledgeBase(content)
	}
	return KnowledgeBase(secEnvironment + "\n" + content)
}

// Load reads the per-project KB for projectRoot from the default data dir,
// verbatim (no migration transform).
//
// Legacy: this verbatim reader is kept so the pre-v0.12 recall call sites keep
// compiling unchanged; K3 migrates those call sites to LoadProject
// (migration-aware) and LoadGlobal. New code should use LoadProject.
func Load(projectRoot string) KnowledgeBase {
	return LoadFrom(DefaultRoot(), projectRoot)
}

// LoadFrom is Load against an explicit root (for tests / non-default data dirs).
//
// Legacy: verbatim reader; see Load. K3 moves callers to LoadProject.
func LoadFrom(root, projectRoot string) KnowledgeBase {
	b, err := os.ReadFile(Path(root, projectRoot))
	if err != nil {
		return ""
	}
	return KnowledgeBase(b)
}

// AppendTo appends a distilled fact as a flat "- <fact>" bullet to the
// per-project knowledge base under an explicit root.
//
// Legacy: the flat (unsectioned) writer, kept so doRemember keeps compiling. K2
// replaces the call site with Append(root, projectRoot, KindEnvironment, "",
// fact) and the two-set taxonomy; the flat files this still produces migrate
// lazily via LoadProject / the first sectioned Append. New code should use Append.
func AppendTo(root, projectRoot, fact string) error {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return nil
	}
	fact = strings.ReplaceAll(fact, "\n", " ")
	p := Path(root, projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("- " + fact + "\n")
	return err
}
