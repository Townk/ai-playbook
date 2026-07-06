package author

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/agentstream"
)

// EnvVarNote is the model's rationale for an environment variable it judges
// relevant to a playbook: the variable NAME and a one-line WHY. The value is NOT
// carried here — the driver fills it from ground truth (and redacts it) in a
// later stage; the why is recorded as-is (a rationale, never a secret).
type EnvVarNote struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// Metadata is the four model-supplied classification fields for a finished
// playbook (spec §A/§B). The model classifies the produced document; everything
// else in the front matter (name, slug, provenance, env values) is assembled
// programmatically in later stages.
type Metadata struct {
	// Description is a one-line, imperative summary of the playbook.
	Description string `json:"description"`
	// Category is a coarse classification, e.g. "Android / build".
	Category string `json:"category"`
	// Tags is a keyword array for browsing/searching the library.
	Tags []string `json:"tags"`
	// ImportantEnvVars are env vars the model judges important to THIS playbook,
	// each a name + one-line why. Values are filled (and redacted) later.
	ImportantEnvVars []EnvVarNote `json:"importantEnvVars"`
}

// MetadataPrompt instructs the model to classify a FINISHED playbook into the
// four front-matter fields and return ONLY a JSON object matching the spec §B
// schema — no prose, no markdown fence. doc is the load-bearing input: the
// complete playbook (after any amends) the model classifies what was actually
// produced, not a mid-stream guess.
func MetadataPrompt(doc string) string {
	var b strings.Builder
	b.WriteString("You are classifying a finished Literate-Config playbook into front-matter\n")
	b.WriteString("metadata. Read the playbook below and respond with ONLY a single JSON object\n")
	b.WriteString("matching this exact schema:\n\n")
	b.WriteString("{\n")
	b.WriteString(`  "description": "one line, imperative — what this playbook sets up/does",` + "\n")
	b.WriteString(`  "category": "a coarse classification, e.g. \"Android / build\"",` + "\n")
	b.WriteString(`  "tags": ["keyword", ...],` + "\n")
	b.WriteString(`  "importantEnvVars": [` + "\n")
	b.WriteString(`    { "name": "ENV_VAR_NAME", "why": "one-line reason this var matters to THIS playbook" }` + "\n")
	b.WriteString("  ]\n")
	b.WriteString("}\n\n")
	b.WriteString("Field rules:\n")
	b.WriteString("- description: ONE line, imperative voice.\n")
	b.WriteString("- category: a short classification string (e.g. \"Android / build\").\n")
	b.WriteString("- tags: an array of short keyword strings.\n")
	b.WriteString("- importantEnvVars: an array of {name, why} objects for the environment\n")
	b.WriteString("  variables important to THIS playbook — each the var NAME and a one-line WHY\n")
	b.WriteString("  it matters. Include vars that matter even if they are not textually in a\n")
	b.WriteString("  code block. Do NOT include any value; do NOT invent vars. Empty array if none.\n\n")
	b.WriteString("Respond with the JSON object ONLY: no prose, no explanation, no markdown code\n")
	b.WriteString("fence — just the raw JSON.\n\n")
	b.WriteString("## Playbook\n")
	b.WriteString(doc)
	b.WriteString("\n")
	return b.String()
}

// PlaybookMetadata runs a post-generation classification call over the OWNED
// harness invocation (the same seam as FinalPlaybook/AuthorEvents, injectable via
// AuthorOptions.Command) and returns the parsed Metadata for doc.
//
// This is a PURE text→JSON classification call: it attaches NO MCP tools backend
// (no --mcp-config), since the model needs no run/ask/remember access to classify
// a finished document — doc is the only load-bearing input. The system prompt is
// MetadataPrompt(doc); the user message is a short trigger.
//
// Mechanism: it drains the harness's normalized event stream and takes the Final
// result text (falling back to accumulated TextDelta if a harness emits no Final),
// tolerates a stray ```json fence or surrounding whitespace by extracting the
// outer {...}, and json.Unmarshals into Metadata. On unparseable output it retries
// ONCE; if it still fails it returns a clear error (a later caller falls back to a
// metadata-less front matter — this just surfaces the failure).
func PlaybookMetadata(doc string, opts AuthorOptions) (Metadata, error) {
	// A classification call needs no tools backend; never attach --mcp-config.
	opts.ToolArgv = nil
	// Bound the call (A5a): like classify, metadata gates the finish of every
	// authoring run and is meant to complete in a few seconds, so a stalled
	// harness must not hang the caller forever. A caller that already set a
	// Timeout (e.g. a test forcing a short deadline) keeps it.
	if opts.Timeout <= 0 {
		opts.Timeout = defaultCallTimeout
	}
	// Structured one-shot JSON — no reasoning needed; disable thinking (cuts ~4-6s).
	opts.NoThinking = true
	sys := MetadataPrompt(doc)
	const user = "Classify the playbook above. Respond with the JSON object only."

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := runMetadataOnce(sys, user, opts)
		if err != nil {
			lastErr = err
			continue
		}
		meta, perr := parseMetadata(out)
		if perr != nil {
			lastErr = perr
			continue
		}
		return meta, nil
	}
	return Metadata{}, fmt.Errorf("playbook metadata classification failed after retry: %w", lastErr)
}

// runMetadataOnce performs a single owned-harness invocation and returns the
// authoritative result text: the Final event's text, or the accumulated TextDelta
// if no Final is emitted (mirroring agentstream fanout's body fallback).
func runMetadataOnce(systemPrompt, userMessage string, opts AuthorOptions) (string, error) {
	events, wait, err := RunHarnessEvents(systemPrompt, userMessage, opts)
	if err != nil {
		return "", err
	}
	var final, deltas strings.Builder
	haveFinal := false
	for e := range events {
		switch e.Kind {
		case agentstream.Final:
			final.WriteString(e.Text)
			haveFinal = true
		case agentstream.TextDelta:
			deltas.WriteString(e.Text)
			// Live tap: surface the accumulating assistant text as it streams (the
			// classify pass feeds this to the float's thinking line). nil → no-op.
			if opts.OnText != nil {
				opts.OnText(deltas.String())
			}
		}
	}
	if werr := wait(); werr != nil {
		return "", werr
	}
	if haveFinal {
		return final.String(), nil
	}
	return deltas.String(), nil
}

// parseMetadata tolerates a stray ```json fence or surrounding whitespace/prose by
// extracting the outer {...} object, then json.Unmarshals into Metadata.
func parseMetadata(out string) (Metadata, error) {
	jsonStr := extractJSONObject(out)
	if jsonStr == "" {
		return Metadata{}, fmt.Errorf("no JSON object found in harness output: %q", strings.TrimSpace(out))
	}
	var meta Metadata
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		return Metadata{}, fmt.Errorf("unmarshal metadata JSON: %w (raw: %q)", err, jsonStr)
	}
	return meta, nil
}

// extractJSONObject returns the substring from the first '{' to the last '}'
// (inclusive), which strips a ```json fence, surrounding whitespace, or stray
// prose around an otherwise-valid JSON object. Returns "" if no braces are found.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
