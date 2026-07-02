// Package frontmatter is a pure, self-contained assembler that turns a
// finished playbook body plus model-supplied classification metadata, an
// injected environment lookup, and programmatic provenance into a YAML
// front-matter block.
//
// It deliberately does NOT import the author package (to avoid coupling) and
// performs no I/O: the environment is read through a func(name)(value,bool)
// lookup supplied by the caller, so every function here is deterministic and
// testable. Driver/persistence/render wiring lives in later stages.
package frontmatter

import (
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// envRefRe matches env-var references in a playbook body. It captures an
// env-var-style name (uppercase convention) appearing in any of these forms:
//
//	$VAR          plain expansion
//	${VAR}        braced expansion
//	export VAR=   export assignment
//	VAR=          bare assignment
//
// This is reference-matching, not bare-name substring matching: a name only
// counts when it is sigil-prefixed or in assignment position, so prose words
// (HOME, USER, LANG written as plain text) do not false-positive.
var envRefRe = regexp.MustCompile(
	`\$\{([A-Z_][A-Z0-9_]*)\}` + // ${VAR}
		`|\$([A-Z_][A-Z0-9_]*)` + // $VAR
		`|(?m)(?:^|[;&|]|\bexport\s+)\s*([A-Z_][A-Z0-9_]*)=`, // [export] VAR=
)

// ScanEnvRefs finds env-var references in the playbook body and returns the
// unique env-var-style names it discovers, sorted for stable output. Only
// reference forms ($VAR, ${VAR}, export VAR=, VAR=) are matched; bare prose
// words are ignored.
func ScanEnvRefs(body string) []string {
	seen := map[string]struct{}{}
	for _, m := range envRefRe.FindAllStringSubmatch(body, -1) {
		// Exactly one of the capture groups is populated per match.
		for _, g := range m[1:] {
			if g != "" {
				seen[g] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// secretNameRe matches variable names that conventionally hold secrets. The
// match is case-insensitive and substring (e.g. GITHUB_TOKEN, API_KEY,
// DB_PASSWORD, AWS_SECRET_ACCESS_KEY, *_CREDENTIAL all hit).
var secretNameRe = regexp.MustCompile(`(?i)(TOKEN|KEY|SECRET|PASS|CREDENTIAL)`)

// redactedMask is the literal emitted in place of a secret value.
const redactedMask = "<redacted>"

// Redact masks the value when the name conventionally holds a secret OR the
// value itself looks like a secret (long, no spaces, high character-class
// diversity). The masked output is the literal "<redacted>". Otherwise the
// value is returned unchanged.
func Redact(name, value string) (out string, redacted bool) {
	if secretNameRe.MatchString(name) {
		return redactedMask, true
	}
	if looksLikeSecret(value) {
		return redactedMask, true
	}
	return value, false
}

// IsRedactedMask reports whether s is exactly the placeholder Redact substitutes
// for a sensitive value. Callers use it to detect a front-matter default that was
// already redacted at build time.
func IsRedactedMask(s string) bool { return s == redactedMask }

// looksLikeSecret is a best-effort heuristic for opaque secret-like values: a
// single whitespace-free token, reasonably long, drawing from a mix of
// character classes (so it is not a plain path or a single-class id). Paths,
// short ids, and human-readable strings (which contain spaces or are short or
// single-class) are left alone.
func looksLikeSecret(value string) bool {
	const minLen = 20
	if len(value) < minLen {
		return false
	}
	if strings.ContainsAny(value, " \t\n") {
		return false // human-readable / multi-token, not an opaque secret
	}
	// A filesystem path is not a secret even when long: it is dominated by
	// path separators and lowercase. Bail out on the obvious path shapes.
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") ||
		strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return false
	}

	var lower, upper, digit, other bool
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, c := range []bool{lower, upper, digit, other} {
		if c {
			classes++
		}
	}
	// Mixed-charset (>=3 classes) long whitespace-free token => high entropy.
	return classes >= 3
}

// nonPortableEnv is the set of env-var names that are never shareable playbook
// config: they are machine-, user-, session-, or shell-specific and would leak
// or mislead when a playbook is read on another team member's machine. BuildEnv
// SKIPS any name in this set unconditionally — even when it was referenced in
// the body or flagged in the model's notes. HOME is the motivating case: it must
// never appear in a shared playbook.
var nonPortableEnv = map[string]struct{}{
	"HOME":    {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"PWD":     {},
	"OLDPWD":  {},
	"TERM":    {},
	"PATH":    {},
	"TMPDIR":  {},
}

// NormalizeHome rewrites a LEADING home-directory path prefix in s to "~" for
// portability: when s equals home it returns "~"; when s begins with home+"/" it
// returns "~" followed by the remainder (preserving the separator); otherwise s
// is returned unchanged. Only a leading path prefix is rewritten — a home string
// that merely appears mid-string is left intact. An empty home disables
// normalization (s is returned unchanged).
func NormalizeHome(s, home string) string {
	if home == "" {
		return s
	}
	if s == home {
		return "~"
	}
	if strings.HasPrefix(s, home+"/") {
		return "~" + s[len(home):]
	}
	return s
}

// EnvValue is one entry in the front-matter env map: the (possibly redacted)
// value and an optional model-supplied rationale. Why is omitted for vars
// discovered only by the body scan.
type EnvValue struct {
	Value string `yaml:"value"`
	Why   string `yaml:"why,omitempty"`
}

// BuildEnv constructs the front-matter env map from the union of body-scanned
// reference names and the keys of notes (the model's importantEnvVars, mapped
// to name->why by the caller). For each name the injected lookup supplies the
// ground-truth value; names absent from the env are omitted. Values are passed
// through Redact, and Why is taken from notes (empty for scan-only vars, where
// omitempty drops it on marshal).
//
// Non-portable/universal vars (nonPortableEnv: HOME/USER/PATH/…) are SKIPPED
// unconditionally — even when referenced in the body or flagged in notes — since
// playbooks are shared across team members and those values are never shareable
// config. Each captured value is then passed through NormalizeHome(value, home)
// so a leading home-dir path becomes "~/…" for portability; an empty home
// disables that rewrite (the denylist still applies).
func BuildEnv(refNames []string, notes map[string]string, lookup func(string) (string, bool), home string) map[string]EnvValue {
	names := map[string]struct{}{}
	for _, n := range refNames {
		names[n] = struct{}{}
	}
	for n := range notes {
		names[n] = struct{}{}
	}

	env := make(map[string]EnvValue, len(names))
	for name := range names {
		if _, deny := nonPortableEnv[name]; deny {
			continue // non-portable/universal var: never shareable playbook config
		}
		raw, ok := lookup(name)
		if !ok {
			continue // a var absent from the env is omitted (§C)
		}
		value, _ := Redact(name, raw)
		value = NormalizeHome(value, home)
		env[name] = EnvValue{Value: value, Why: notes[name]}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// FrontMatter is the assembled playbook front matter. Name and the
// programmatic provenance fields are inputs supplied by the caller; the
// classification fields originate from the model.
type FrontMatter struct {
	Name         string              `yaml:"name"`
	Description  string              `yaml:"description,omitempty"`
	Category     string              `yaml:"category,omitempty"`
	Tags         []string            `yaml:"tags,omitempty"`
	Env          map[string]EnvValue `yaml:"env,omitempty"`
	Created      string              `yaml:"created,omitempty"`
	ProjectRoot  string              `yaml:"project_root,omitempty"`
	ProjectBound bool                `yaml:"project_bound,omitempty"`
	Request      string              `yaml:"request,omitempty"`
}

// Assemble marshals the front matter to a YAML document fenced by "---"
// delimiters: "---\n<yaml>---\n". yaml.v3 handles quoting/escaping of colons,
// paths, and the nested env map.
func Assemble(fm FrontMatter) string {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	// Marshal cannot fail for these plain struct types; ignore the error and
	// let any panic surface in tests rather than silently dropping fields.
	_ = enc.Encode(fm)
	_ = enc.Close()
	return "---\n" + b.String() + "---\n"
}

// Prepend places the assembled front matter above the playbook body, separated
// by a blank line.
func Prepend(fm FrontMatter, body string) string {
	return Assemble(fm) + "\n" + body
}

// Parse splits a leading "---\n…\n---" front-matter block off the front of
// content. When content begins with such a block, the fenced YAML is unmarshaled
// into a FrontMatter and the remainder (after the closing fence and a single
// following blank line, when present) is returned as body with ok=true. When
// content does NOT begin with a front-matter fence (old saved files, fresh
// drafts, or a "---" that only appears inside the body / a fenced code block),
// Parse returns the zero FrontMatter, content unchanged, and ok=false.
//
// Only a block at the very start counts: the opening fence must be the first
// line (a leading "---\n"), so a "---" sitting inside a fenced code block in the
// body is never mistaken for front matter. Parse round-trips with
// Assemble/Prepend.
func Parse(content string) (fm FrontMatter, body string, ok bool) {
	// The opening fence must be the very first line: "---" followed by a newline.
	if !strings.HasPrefix(content, "---\n") {
		return FrontMatter{}, content, false
	}
	rest := content[len("---\n"):]

	// Find the closing fence: a line that is exactly "---". Scan line by line so
	// the first such line terminates the block (cache.Body-compatible: stop at the
	// first closing fence, leaving any inner playbook FM intact).
	idx := indexClosingFence(rest)
	if idx.yamlEnd < 0 {
		// No closing fence → not a well-formed front-matter block.
		return FrontMatter{}, content, false
	}
	yamlDoc := rest[:idx.yamlEnd]
	body = rest[idx.bodyStart:]
	// Drop a single blank line directly after the closing fence (the separator
	// Prepend inserts), so the returned body starts at the real content.
	body = strings.TrimPrefix(body, "\n")

	if err := yaml.Unmarshal([]byte(yamlDoc), &fm); err != nil {
		return FrontMatter{}, content, false
	}
	return fm, body, true
}

// fenceLoc records the YAML span end and the body start offset within the text
// scanned after the opening fence.
type fenceLoc struct {
	yamlEnd   int // end of the YAML document (exclusive; before the closing fence)
	bodyStart int // start of the body (after the closing fence line)
}

// indexClosingFence finds the first line equal to "---" in s and returns the
// offsets bracketing it; it returns a sentinel with negative fields when none is
// found. s is the text AFTER the opening fence.
func indexClosingFence(s string) fenceLoc {
	off := 0
	for off <= len(s) {
		nl := strings.IndexByte(s[off:], '\n')
		var line string
		var lineEnd int // offset just past this line's trailing newline (or len(s))
		if nl < 0 {
			line = s[off:]
			lineEnd = len(s)
		} else {
			line = s[off : off+nl]
			lineEnd = off + nl + 1
		}
		if line == "---" {
			return fenceLoc{yamlEnd: off, bodyStart: lineEnd}
		}
		if nl < 0 {
			break
		}
		off = lineEnd
	}
	return fenceLoc{yamlEnd: -1, bodyStart: -1}
}
