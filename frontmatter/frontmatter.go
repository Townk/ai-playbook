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
func BuildEnv(refNames []string, notes map[string]string, lookup func(string) (string, bool)) map[string]EnvValue {
	names := map[string]struct{}{}
	for _, n := range refNames {
		names[n] = struct{}{}
	}
	for n := range notes {
		names[n] = struct{}{}
	}

	env := make(map[string]EnvValue, len(names))
	for name := range names {
		raw, ok := lookup(name)
		if !ok {
			continue // a var absent from the env is omitted (§C)
		}
		value, _ := Redact(name, raw)
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
	Name        string              `yaml:"name"`
	Description string              `yaml:"description,omitempty"`
	Category    string              `yaml:"category,omitempty"`
	Tags        []string            `yaml:"tags,omitempty"`
	Env         map[string]EnvValue `yaml:"env,omitempty"`
	Created     string              `yaml:"created,omitempty"`
	ProjectRoot string              `yaml:"project_root,omitempty"`
	Request     string              `yaml:"request,omitempty"`
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
