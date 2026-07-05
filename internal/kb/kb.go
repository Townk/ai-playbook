// Package kb is the two-set knowledge-base store (ADR-0011): distilled facts
// the `remember` tool writes and every authoring-shaped call recalls.
//
// Two knowledge sets, four kinds (Kind / KindSystem / KindUser /
// KindEnvironment / KindTopic):
//
//   - GLOBAL — one file shared across every project, sectioned "## System"
//     (machine/tooling truths) and "## User" (who the user is / prefers):
//
//     $root/knowledge.md
//
//   - PROJECT — one file per project, sectioned "## Environment" (this
//     project's setup) and "## Topics" (domain-specific lessons, each a
//     "### <topic>" subsection):
//
//     $root/projects/<project_key>/knowledge.md
//
// where project_key = SHA-1 (lowercase hex) of the project-root path string
// and $root is the same data dir the cache uses: AI_PLAYBOOK_DATA_DIR, else
// ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook. Each project file also
// carries a `<!-- meta: project-root: <path> -->` line (sections.go) so a
// browser (`kb list`/`search`) can show the real project path instead of the
// opaque key.
//
// Append (sections.go) routes a fact to its file + section by Kind, with
// write-dedup (an exact-normalized duplicate within the target
// section/subsection is a silent no-op) and lazy migration (migrate.go): a
// pre-K1 flat, unsectioned file is read as if its bullets lived under
// "## Environment", and the first sectioned write rewrites it in place.
package kb

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
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

// Capped enforces the hard read-time tail-cap on a single knowledge blob: when
// content exceeds limit bytes it is truncated at a LINE (bullet) boundary — never
// mid-line — and a one-line note is written to stderr. This is the vestigial
// safety against a pathologically large hand-edited file, NOT the budget
// mechanism (that is write-time compaction, K4); limit is 8× the per-file budget
// so a normal file never trips it.
//
// The HEAD is kept, not the tail: both files are section-ordered (global
// System→User, project Environment→Topics), so the head preserves the stable,
// foundational sets and drops the long, most-likely-stale tail. A limit <= 0 (or
// content already within limit) returns content unchanged and writes nothing.
func Capped(content KnowledgeBase, limit int) KnowledgeBase {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	s := string(content)
	// Cut at the last newline at or before the limit so a whole bullet/line is
	// never split. If there is no earlier newline (one pathological long line),
	// fall back to a hard byte cut.
	cut := strings.LastIndexByte(s[:limit], '\n')
	if cut <= 0 {
		cut = limit
	}
	fmt.Fprintf(os.Stderr, "ai-playbook: knowledge file exceeds the %d-byte read cap; truncated at recall (kept head)\n", limit)
	return KnowledgeBase(strings.TrimRight(s[:cut], "\n") + "\n")
}
