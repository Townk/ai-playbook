// Package cache is the Go port of the shell `ai-assist-cache` helper: a
// context-hash response cache for produced playbooks/answers/commands.
//
// It faithfully reproduces the shell's hashing (same sha256 inputs and order)
// and the on-disk layout, so a Go writer and the legacy shell reader (and vice
// versa) agree on entry paths during the strangler migration:
//
//	$root/cache/<context_hash>/<request_hash>.md            — entry (YAML front matter + body)
//	$root/cache/<context_hash>/<request_hash>.request.json  — sidecar (original request.json)
//
// The store root:
//
//	AI_PLAYBOOK_DATA_DIR                                  (highest priority)
//	${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook      (default)
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Request is the subset of the request needed to compute the context hash. It
// mirrors the REQ_* environment variables the shell `context-hash` subcommand
// reads. ProjectRoot falls back to CWD when empty (as `${REQ_PROJECT_ROOT:-${REQ_CWD:-}}`).
type Request struct {
	ProjectRoot string // REQ_PROJECT_ROOT
	CWD         string // REQ_CWD (fallback for ProjectRoot)
	CommandText string // REQ_COMMAND_TEXT
	CommandExit string // REQ_COMMAND_EXIT (string; "" means absent)
	Scrollback  string // REQ_SCROLLBACK (raw, un-normalized)
}

// Cache is a handle to a store root.
type Cache struct {
	Root string
}

// Open returns a Cache rooted at the data dir:
// AI_PLAYBOOK_DATA_DIR, else ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook.
func Open() *Cache {
	return &Cache{Root: DefaultRoot()}
}

// DefaultRoot resolves the store root.
func DefaultRoot() string {
	if v := os.Getenv("AI_PLAYBOOK_DATA_DIR"); v != "" {
		return v
	}
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdg, "ai-playbook")
}

// sha256hex returns the lowercase hex sha256 of s.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// projectRoot resolves ${REQ_PROJECT_ROOT:-${REQ_CWD:-}}.
func (r Request) projectRoot() string {
	if r.ProjectRoot != "" {
		return r.ProjectRoot
	}
	return r.CWD
}

// ContextHash reproduces the shell `context-hash` subcommand.
//
// Include the last command + its (normalized) output ONLY when it FAILED
// (non-zero exit) — that is the error-diagnosis context. A successful or absent
// last command keys on the PROJECT ONLY, so the bucket stays stable regardless
// of what the user last ran or cd'd through.
//
// Faithful to the shell, which uses (no trailing newline):
//
//	printf '%s\n%s\n%s\n%s\n%s' v1 <root> <cmd> <exit> <normalized_scrollback>  (failure)
//	printf '%s\n%s'             v1 <root>                                        (otherwise)
func ContextHash(r Request) string {
	root := r.projectRoot()
	if r.CommandExit != "" && r.CommandExit != "0" {
		// The shell captures the normalized scrollback via $(...) command
		// substitution, which strips ALL trailing newlines, then interpolates it
		// as the final printf field (no trailing newline). Mirror that here.
		norm := strings.TrimRight(NormalizeScrollback(r.Scrollback), "\n")
		return sha256hex(strings.Join([]string{"v1", root, r.CommandText, r.CommandExit, norm}, "\n"))
	}
	return sha256hex(strings.Join([]string{"v1", root}, "\n"))
}

// RequestHash reproduces the shell `request-hash` subcommand: trim leading and
// trailing whitespace (only space, tab, newline — matching the shell's
// `[! $'\t'$'\n']` plus space), then sha256.
func RequestHash(text string) string {
	trimmed := strings.Trim(text, " \t\n")
	return sha256hex(trimmed)
}

// ansiCSI matches ESC[ … <final letter> (CSI sequences, e.g. colors/cursor).
// Mirrors the shell sed `s/\x1b\[[0-9;?]*[A-Za-z]//g`.
var ansiCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// ansiCharset matches ESC ( or ESC ) followed by one of A B 0 1 2 (charset
// selection). Mirrors the shell sed `s/\x1b[()][AB012]//g`.
var ansiCharset = regexp.MustCompile("\x1b[()][AB012]")

// ansiOther matches ESC followed by any single non-'[' byte. Mirrors the shell
// sed `s/\x1b[^[]//g`. Applied AFTER the two patterns above (sed applies its -e
// expressions left-to-right per line), so a CSI/charset ESC is already gone and
// only stray standalone ESC sequences remain. A trailing bare ESC at end of a
// line (no following byte) is left as-is, matching sed's [^[] requiring a char.
var ansiOther = regexp.MustCompile("\x1b[^\\[]")

// trailingSpace matches POSIX [[:space:]]* at end of line. The shell sed uses
// `s/[[:space:]]*$//` (per line). In Go we strip per line with this set.
const posixSpace = " \t\r\n\v\f"

// NormalizeScrollback reproduces the shell `_normalize_scrollback`: strip
// ANSI/CSI escape sequences, trim trailing whitespace per line, collapse runs of
// 2+ blank lines to one, and drop leading/trailing blank lines.
//
// The shell pipeline is `sed (3 strip exprs + trailing-trim) | awk (blank-line
// collapse)`. We reproduce both stages on a line basis.
//
// Output has each retained line followed by '\n' (awk's print), with NO trailing
// blank lines and NO leading blank lines.
func NormalizeScrollback(s string) string {
	// sed operates per line. Split keeping the same line set sed would see.
	// sed reads lines split on '\n'; a trailing newline yields a final empty
	// record that sed does not re-emit, so trim a single trailing '\n' for the
	// split to mirror sed's record set.
	body := s
	lines := strings.Split(body, "\n")
	// If the input ended with '\n', the split produced a trailing "" element
	// that does not correspond to a real sed record; drop it (sed would not
	// process an empty final record after the last newline).
	if len(lines) > 0 && strings.HasSuffix(body, "\n") {
		lines = lines[:len(lines)-1]
	}

	// Stage 1 (sed): strip escapes + trim trailing whitespace, per line.
	for i, ln := range lines {
		ln = ansiCSI.ReplaceAllString(ln, "")
		ln = ansiCharset.ReplaceAllString(ln, "")
		ln = ansiOther.ReplaceAllString(ln, "")
		ln = strings.TrimRight(ln, posixSpace)
		lines[i] = ln
	}

	// Stage 2 (awk): collapse blank runs, drop leading/trailing blanks.
	//   /^[[:space:]]*$/ { blanks++; next }
	//   { if (blanks>0 && out>0) print ""; blanks=0; out++; print }
	var out []string
	blanks := 0
	emitted := 0
	for _, ln := range lines {
		if isBlank(ln) {
			blanks++
			continue
		}
		if blanks > 0 && emitted > 0 {
			out = append(out, "")
		}
		blanks = 0
		emitted++
		out = append(out, ln)
	}
	if len(out) == 0 {
		return ""
	}
	// awk's print appends '\n' after every line, including the last.
	return strings.Join(out, "\n") + "\n"
}

// isBlank reports whether a line is empty or only POSIX whitespace, matching
// awk's /^[[:space:]]*$/.
func isBlank(s string) bool {
	return strings.TrimLeft(s, posixSpace) == ""
}

// Lookup returns the entry path for (ctx, req) and true if the .md entry exists.
func (c *Cache) Lookup(ctx, req string) (string, bool) {
	entry := filepath.Join(c.Root, "cache", ctx, req+".md")
	if fi, err := os.Stat(entry); err == nil && !fi.IsDir() {
		return entry, true
	}
	return "", false
}

// RequestFile returns the sidecar request.json path for (ctx, req) and true if
// it exists.
func (c *Cache) RequestFile(ctx, req string) (string, bool) {
	sidecar := filepath.Join(c.Root, "cache", ctx, req+".request.json")
	if fi, err := os.Stat(sidecar); err == nil && !fi.IsDir() {
		return sidecar, true
	}
	return "", false
}

// Store writes the cache entry atomically (front matter + body via mktemp→rename)
// and, when requestJSON is non-empty, saves it alongside as <req>.request.json
// (best-effort). It returns the entry path.
//
// extras are additional YAML front-matter fields (e.g. request/project_root/
// harness from the shell's meta file). They are emitted after the fixed fields,
// sorted by key for determinism (the shell iterates an unordered assoc array;
// sorting makes our output stable and testable without changing semantics).
func (c *Cache) Store(ctx, req, kind, body string, extras map[string]string, requestJSON string) (string, error) {
	cacheDir := filepath.Join(c.Root, "cache", ctx)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	entry := filepath.Join(cacheDir, req+".md")
	iso := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("schema: ai-playbook-cache/v1\n")
	fmt.Fprintf(&b, "kind: %s\n", kind)
	fmt.Fprintf(&b, "context_hash: %s\n", ctx)
	fmt.Fprintf(&b, "request_hash: %s\n", req)
	fmt.Fprintf(&b, "created_at: %s\n", iso)
	for _, k := range sortedKeys(extras) {
		fmt.Fprintf(&b, "%s: %s\n", k, extras[k])
	}
	b.WriteString("---\n")
	b.WriteString(body)

	if err := writeAtomic(cacheDir, entry, []byte(b.String())); err != nil {
		return "", err
	}

	// Best-effort sidecar (faithful regenerate context).
	if requestJSON != "" {
		sidecar := filepath.Join(cacheDir, req+".request.json")
		_ = writeAtomic(cacheDir, sidecar, []byte(requestJSON))
	}
	return entry, nil
}

// Body returns the entry body with the leading YAML front-matter block stripped,
// mirroring the shell `body` subcommand: if the first line is not "---", the file
// is returned as-is; otherwise content from after the SECOND "---" line.
func Body(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 {
		return content
	}
	if strings.TrimRight(lines[0], "\n") != "---" {
		return content
	}
	// Find the closing "---" (the next line equal to "---").
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\n") == "---" {
			return strings.Join(lines[i+1:], "")
		}
	}
	// No closing fence: nothing after front matter.
	return ""
}

// Field returns the YAML front-matter value for key from the leading ---…---
// block, mirroring the shell `field` subcommand. ok is false if absent.
func Field(content, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", false
	}
	prefix := key + ": "
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return "", false // end of front matter
		}
		if strings.HasPrefix(lines[i], prefix) {
			return lines[i][len(prefix):], true
		}
	}
	return "", false
}

// writeAtomic writes data to a temp file in dir then renames over dest.
func writeAtomic(dir, dest string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// sortedKeys returns the keys of m in lexical order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid importing sort for a tiny map
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
