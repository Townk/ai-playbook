package author

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/Townk/ai-playbook/internal/draft"
)

// TestPiHarness_ArgvCharacterization pins the EXACT ordered pi argv for every
// invocation shape RunHarnessEvents produces (normal/bare × tools/no-tools ×
// model set/empty). The flag set was live-verified against pi 0.80.3 (the
// characterization probes): -p --mode json --no-session --no-extensions on
// every path; --no-tools ONLY when no tool transport is attached (with the
// transport, inv.ToolArgv carries --no-builtin-tools + --extension instead —
// the two must stay strictly paired, see the Argv rationale); --thinking
// always explicit; bare additionally strips context files, skills, and prompt
// templates and REPLACES the system prompt.
func TestPiHarness_ArgvCharacterization(t *testing.T) {
	h := piHarness{}
	common := []string{"-p", "--mode", "json", "--no-session", "--no-extensions"}
	app := func(rest ...string) []string { return append(append([]string{}, common...), rest...) }
	toolArgv := []string{"--no-builtin-tools", "--extension", "/tmp/dir/ai-playbook-pi-extension.ts"}

	cases := []struct {
		name string
		inv  Invocation
		want []string
	}{
		{
			name: "normal, no model, no tools",
			inv:  Invocation{},
			want: app("--no-tools", "--thinking", "medium",
				"--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, model, no tools",
			inv:  Invocation{Model: "qwen3.7-plus", Thinking: "high"},
			want: app("--no-tools", "--thinking", "high", "--model", "qwen3.7-plus",
				"--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, model, tools",
			inv:  Invocation{Model: "qwen3.7-plus", Thinking: "medium", ToolArgv: toolArgv},
			want: app("--thinking", "medium", "--model", "qwen3.7-plus",
				"--no-builtin-tools", "--extension", "/tmp/dir/ai-playbook-pi-extension.ts",
				"--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, no model, tools",
			inv:  Invocation{ToolArgv: toolArgv},
			want: app("--thinking", "medium",
				"--no-builtin-tools", "--extension", "/tmp/dir/ai-playbook-pi-extension.ts",
				"--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "bare, model, no tools (the classify shape)",
			inv:  Invocation{Model: "qwen3.7-plus", Bare: true, Thinking: "off"},
			want: app("--no-tools", "--thinking", "off", "--model", "qwen3.7-plus",
				"--no-context-files", "--no-skills", "--no-prompt-templates",
				"--system-prompt", "SYS", "USER"),
		},
		{
			name: "bare, no model, no tools",
			inv:  Invocation{Bare: true, Thinking: "off"},
			want: app("--no-tools", "--thinking", "off",
				"--no-context-files", "--no-skills", "--no-prompt-templates",
				"--system-prompt", "SYS", "USER"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.Argv("SYS", "USER", tc.inv)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Argv:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestPiThinkingLevel pins the config→--thinking mapping: the shared config
// levels map directly, pi's two extra native levels (minimal, xhigh) pass
// through, and everything unrecognized — including claude-style numeric
// budgets, which pi has no equivalent for — falls back to medium (0 → off).
func TestPiThinkingLevel(t *testing.T) {
	cases := map[string]string{
		"off":     "off",
		"none":    "off",
		"0":       "off",
		"minimal": "minimal",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"on":      "medium",
		"":        "medium",
		"8000":    "medium",
		"garbage": "medium",
	}
	for in, want := range cases {
		if got := piThinkingLevel(in); got != want {
			t.Errorf("piThinkingLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPiHarness_Env pins that pi needs NO extra process env on any invocation
// shape — thinking and model are native flags, and the user's provider
// credentials must pass through untouched.
func TestPiHarness_Env(t *testing.T) {
	h := piHarness{}
	for _, inv := range []Invocation{{}, {Thinking: "off"}, {Thinking: "xhigh"}, {Bare: true}} {
		if env := h.Env(inv); len(env) != 0 {
			t.Errorf("Env(%+v) = %v, want none", inv, env)
		}
	}
}

// TestHarnessContract_PiRow pins pi's own contract values: the labels, the
// FULL tier (pi's tool loop schema-validates extension tool arguments before
// execute and reports failures back to the model — the re-ask loop), the
// defaults row (everything on the user's own pi default model; thinking
// medium), and the extension transport attachment shape.
func TestHarnessContract_PiRow(t *testing.T) {
	h, ok := harnessFor("pi")
	if !ok {
		t.Fatal("pi harness not registered")
	}
	if got := h.DisplayName(); got != "pi" {
		t.Errorf("DisplayName = %q, want pi", got)
	}
	if got := h.AdapterName(); got != "pi" {
		t.Errorf("AdapterName = %q, want pi", got)
	}
	if !h.Capabilities().Tools {
		t.Error("pi must be a FULL harness (Tools)")
	}
	if d := HarnessDefaults("pi"); d != (Defaults{Model: "", TriageModel: "", Thinking: "medium"}) {
		t.Errorf("pi defaults row = %+v, want {\"\", \"\", medium}", d)
	}

	dir := t.TempDir()
	// SelfExe deliberately empty: pi's extension dials the tools socket
	// directly, so the transport must NOT require the ai-playbook re-exec path.
	files, argv, err := h.ToolTransport(Invocation{}, "/tmp/tools test.sock", dir)
	if err != nil {
		t.Fatalf("ToolTransport: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %v, want exactly the extension file", files)
	}
	want := []string{"--no-builtin-tools", "--extension", files[0]}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("transport argv = %q, want %q", argv, want)
	}
	if filepath.Dir(files[0]) != dir {
		t.Errorf("extension written to %s, want inside %s", files[0], dir)
	}

	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read transport artifact: %v", err)
	}
	src := string(b)
	// The socket path is spliced in as a QUOTED JS string literal (the space in
	// the path above proves it is not pasted raw), and the placeholder is gone.
	if !strings.Contains(src, `"/tmp/tools test.sock"`) {
		t.Error("extension missing the quoted socket path")
	}
	if strings.Contains(src, "__AI_PLAYBOOK_SOCKET__") {
		t.Error("extension still carries the socket placeholder")
	}
	// The extension registers exactly the mcpserver tool surface.
	for _, tool := range []string{`name: "run"`, `name: "remember"`, `name: "ask"`, `name: "submit_playbook"`} {
		if !strings.Contains(src, tool) {
			t.Errorf("extension missing tool registration %s", tool)
		}
	}

	// An unknown socket path is a caller bug and must fail loudly, never write
	// a transport that dials nothing.
	if _, _, err := h.ToolTransport(Invocation{}, "", t.TempDir()); err == nil {
		t.Error("ToolTransport with no socket path must fail")
	}
}

// TestPiHarness_ToolTransport_SocketPathSpliceRoundTrips pins the splice's
// JS-literal correctness for exotic paths: the socket path is embedded via
// json.Marshal, and a JSON string literal is valid JS with IDENTICAL string
// semantics for every input — unlike Go quoting, whose \a (BEL) is not a JS
// escape (silent corruption) and whose \U… form for non-printable astral runes
// is a JS syntax error. Each case extracts the emitted literal from the
// written extension and parses it back (a JSON unmarshal — the exact grammar
// JS applies to a JSON-shaped literal) asserting it round-trips to the exact
// path.
func TestPiHarness_ToolTransport_SocketPathSpliceRoundTrips(t *testing.T) {
	h := piHarness{}
	paths := []string{
		`/tmp/with"quote/t.sock`,
		`/tmp/with\backslash/t.sock`,
		"/tmp/with\nnewline/t.sock",
		"/tmp/with\abel/t.sock",                    // BEL: the strconv.Quote silent-corruption case
		"/tmp/with\U000E0001astral/t.sock",         // non-printable astral: the strconv.Quote syntax-error case
		"/tmp/with\U0001F600emoji & <html>/t.sock", // printable astral + JSON's HTML-escaped bytes
	}
	for _, p := range paths {
		t.Run(fmt.Sprintf("%q", p), func(t *testing.T) {
			files, _, err := h.ToolTransport(Invocation{}, p, t.TempDir())
			if err != nil {
				t.Fatalf("ToolTransport: %v", err)
			}
			b, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatalf("read extension: %v", err)
			}
			const prefix = "const SOCKET_PATH = "
			src := string(b)
			i := strings.Index(src, prefix)
			if i < 0 {
				t.Fatal("extension missing the SOCKET_PATH const")
			}
			line := src[i+len(prefix):]
			// json.Marshal never emits raw newlines, so the literal ends at the
			// statement's line end.
			if nl := strings.IndexByte(line, '\n'); nl >= 0 {
				line = line[:nl]
			}
			lit := strings.TrimSuffix(strings.TrimSpace(line), ";")
			var got string
			if err := json.Unmarshal([]byte(lit), &got); err != nil {
				t.Fatalf("emitted literal %s does not parse as a JSON/JS string: %v", lit, err)
			}
			if got != p {
				t.Errorf("spliced literal round-trips to %q, want %q", got, p)
			}
		})
	}
}

// TestPiExtension_SubmitPlaybookSchemaParity is the drift guard for the
// extension's hand-mirrored submit_playbook schema: it extracts the strict-JSON
// schema block between the AI_PLAYBOOK_SCHEMA markers in pi_extension.ts and
// compares it — property sets, types, descriptions, required sets, recursively
// — against the schema DERIVED from draft.Playbook by the same jsonschema
// inference the MCP transport serves claude (mcp.ToolHandlerFor[draft.Playbook]
// → jsonschema.For). Any draft.Playbook field/description/required change now
// fails here until the TS mirror is updated, so the model guidance can never
// drift silently between harnesses.
//
// Deliberate normalization (Go-side nil-ability artifacts the TS mirror
// rightly omits): the derived `"type": ["null", X]` counts as X, and
// `additionalProperties`/`$schema` are ignored — pi's model-facing contract is
// the fields, types, and descriptions; the backend's draft.Validate stays
// authoritative for semantics.
func TestPiExtension_SubmitPlaybookSchemaParity(t *testing.T) {
	const begin = "/* AI_PLAYBOOK_SCHEMA_BEGIN */"
	const end = "/* AI_PLAYBOOK_SCHEMA_END */"
	i := strings.Index(piExtensionSource, begin)
	j := strings.Index(piExtensionSource, end)
	if i < 0 || j < 0 || j <= i {
		t.Fatal("pi_extension.ts is missing the AI_PLAYBOOK_SCHEMA markers")
	}
	var tsSchema map[string]any
	if err := json.Unmarshal([]byte(piExtensionSource[i+len(begin):j]), &tsSchema); err != nil {
		t.Fatalf("the marked schema block is not strict JSON (keep it JSON — the extension comment explains): %v", err)
	}

	derived, err := jsonschema.For[draft.Playbook](nil)
	if err != nil {
		t.Fatalf("derive the draft.Playbook schema: %v", err)
	}
	db, err := json.Marshal(derived)
	if err != nil {
		t.Fatalf("marshal derived schema: %v", err)
	}
	var goSchema map[string]any
	if err := json.Unmarshal(db, &goSchema); err != nil {
		t.Fatalf("unmarshal derived schema: %v", err)
	}

	comparePiSchema(t, "playbook", goSchema, tsSchema)
}

// comparePiSchema recursively asserts the TS schema mirrors the derived one at
// path: same normalized type, same description, same required set, the same
// property names (both directions), and matching items schemas.
func comparePiSchema(t *testing.T, path string, derived, ts map[string]any) {
	t.Helper()

	if dt, tt := normalizedSchemaType(derived["type"]), normalizedSchemaType(ts["type"]); dt != tt {
		t.Errorf("%s: type = %q in the TS mirror, want %q (derived)", path, tt, dt)
	}
	dDesc, _ := derived["description"].(string)
	tDesc, _ := ts["description"].(string)
	if dDesc != tDesc {
		t.Errorf("%s: description drift\n  derived: %q\n  mirror:  %q", path, dDesc, tDesc)
	}
	if dReq, tReq := schemaStringSet(derived["required"]), schemaStringSet(ts["required"]); !reflect.DeepEqual(dReq, tReq) {
		t.Errorf("%s: required = %v in the TS mirror, want %v (derived)", path, tReq, dReq)
	}

	dProps, _ := derived["properties"].(map[string]any)
	tProps, _ := ts["properties"].(map[string]any)
	if (dProps == nil) != (tProps == nil) {
		t.Errorf("%s: properties present in one schema only (derived %v, mirror %v)", path, dProps != nil, tProps != nil)
		return
	}
	for name, dv := range dProps {
		tv, ok := tProps[name]
		if !ok {
			t.Errorf("%s: property %q exists in draft.Playbook but is missing from the TS mirror", path, name)
			continue
		}
		comparePiSchema(t, path+"."+name, dv.(map[string]any), tv.(map[string]any))
	}
	for name := range tProps {
		if _, ok := dProps[name]; !ok {
			t.Errorf("%s: property %q exists in the TS mirror but not in draft.Playbook", path, name)
		}
	}

	dItems, dOK := derived["items"].(map[string]any)
	tItems, tOK := ts["items"].(map[string]any)
	if dOK != tOK {
		t.Errorf("%s: items present in one schema only (derived %v, mirror %v)", path, dOK, tOK)
		return
	}
	if dOK {
		comparePiSchema(t, path+"[]", dItems, tItems)
	}
}

// normalizedSchemaType renders a schema "type" for comparison: the derived
// side expresses Go nil-ability as ["null", X] — normalized to X, the value a
// JS caller actually sends.
func normalizedSchemaType(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case []any:
		var rest []string
		for _, m := range tv {
			if s, ok := m.(string); ok && s != "null" {
				rest = append(rest, s)
			}
		}
		return strings.Join(rest, "|")
	default:
		return ""
	}
}

// schemaStringSet renders a "required" value as a sorted string slice (nil for
// absent/empty, so absent and [] compare equal).
func schemaStringSet(v any) []string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, m := range arr {
		if s, ok := m.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
