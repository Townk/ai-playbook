# Structured Playbook Output — Phase A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `create` author a playbook by calling a `submit_playbook` MCP tool whose input schema *is* the playbook, then render the markdown deterministically — eliminating structure drift — while folding the metadata pass into the same call.

**Architecture:** A new `internal/playbook` package holds the Hybrid schema (Go structs with `jsonschema` tags), a deterministic struct→markdown renderer (body only; front matter is still assembled at save), and semantic validation. A `submit_playbook` tool is added to the harness-neutral tools backend (`internal/tools`) and the MCP adapter (`internal/mcpserver`); its input type is `playbook.Playbook` so Claude's tool-use loop enforces the schema. The `create` path authors with a structured prompt, captures the submitted playbook via a new `Deps.OnPlaybook` backend hook, renders it, shows it in the viewer, and injects a `Metadata` seam that returns the captured meta (dropping the separate metadata model pass). project_bound is added to the front matter.

**Tech Stack:** Go; `github.com/modelcontextprotocol/go-sdk v1.6.1` (MCP); `gopkg.in/yaml.v3` (front matter); goldmark (existing renderer/parser); `charm.land/bubbletea/v2` (viewer). Tests are stdlib `testing` with the repo's fake-harness / fake-backend / seam-injection patterns.

## Global Constraints

- Commits: gpg-signed Conventional Commits. **No** `Co-Authored-By`/AI-attribution trailers. `git add` explicit paths (never `-A`). Verify signing via `git log -1 --format=%G?` == `G` (the `gpg --clearsign` precheck false-negatives).
- Abbreviation is **APB** (ai-PlayBook), never AAPB.
- The deterministic renderer's output is the **body only** — NO front matter. Front matter stays assembled at save (`orchestrator.CommitPlaybook` → `buildFrontMatter`). The renderer's output MUST satisfy `ui.ValidatePlaybook` (an H1 `# Playbook — <title>` line + ≥1 runnable fenced block).
- Fenced-block tag format the renderer MUST emit, matching `internal/ui/block.go parseFenceInfo` byte-for-byte: ```` ```<lang> {id=<id> needs=<a,b> rollback=<id>} ```` for runnable blocks, ```` ```<lang> {static} ```` for static blocks. Tokens inside `{…}` are space-separated; `needs` is comma-separated with no spaces.
- Schema dedup decision: the top-level **`title` IS the playbook name** (H1 = `# Playbook — <title>`; the saved front-matter `name` is derived from that H1 by the existing `PlaybookName`). The `meta` block therefore carries **no `name`** field — only `description`, `category`, `tags`, `project_bound`.
- `project_bound` (bool) is added to `frontmatter.FrontMatter` and `orchestrator.PlaybookMeta`; Phase A WRITES it. Run-time gating of adapt-on-run by `project_bound` (and removing `workdir`) is **Phase B — do NOT touch adapt/escalate/regenerate here**.
- Do NOT migrate escalate/adapt/regenerate/followup. Only `create` switches to structured authoring in Phase A.
- Module path is `github.com/Townk/ai-playbook`. Run `make lint` and `gofmt -l` clean; tests with `-race` where the package has concurrency.

---

### Task 1: Playbook schema types

**Files:**
- Create: `internal/playbook/schema.go`
- Test: `internal/playbook/schema_test.go`

**Interfaces:**
- Produces: types `playbook.Playbook`, `playbook.Section`, `playbook.ContentItem`, `playbook.Step`, `playbook.Meta`. Field names/JSON tags exactly as below — Tasks 2–8 consume them.

- [ ] **Step 1: Write the failing test**

```go
package playbook

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlaybook_JSONRoundTrip(t *testing.T) {
	in := Playbook{
		Title: "Fix the wrapper",
		Intro: "lead prose",
		Sections: []Section{{
			Heading: "Goal & error",
			Content: []ContentItem{
				{Kind: "text", Text: "what happened"},
				{Kind: "code", Lang: "console", Code: "boom", Static: true},
				{Kind: "code", Lang: "bash", Code: "echo fix", ID: "fix", Needs: []string{"diag"}},
			},
		}},
		Verify: &Step{Lang: "bash", Code: "echo ok", Needs: []string{"fix"}},
		Meta:   Meta{Description: "d", Category: "c", Tags: []string{"t"}, ProjectBound: true},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Playbook
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Title != "Fix the wrapper" || len(out.Sections) != 1 || len(out.Sections[0].Content) != 3 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Verify == nil || out.Verify.Needs[0] != "fix" {
		t.Fatalf("verify lost: %+v", out.Verify)
	}
	if !out.Meta.ProjectBound {
		t.Fatalf("project_bound lost")
	}
	// project_bound must serialize as the snake_case key the front matter uses.
	if !strings.Contains(string(b), `"project_bound":true`) {
		t.Fatalf("project_bound json key wrong: %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/playbook/ -run TestPlaybook_JSONRoundTrip`
Expected: FAIL — `internal/playbook` package / types undefined (build error).

- [ ] **Step 3: Write the schema**

```go
// Package playbook defines the structured playbook schema the authoring model
// fills via the submit_playbook MCP tool, plus the deterministic markdown
// renderer and semantic validation. The model returns DATA; we render the
// markdown, so structure (H1, sections, block tags) cannot drift.
package playbook

// Playbook is the whole document. Title is the playbook name (rendered as the H1
// "# Playbook — <Title>" and used as the saved front-matter name). Front matter
// other than the title is carried in Meta and assembled at save time — Render
// emits the BODY only.
type Playbook struct {
	Title    string    `json:"title" jsonschema:"the playbook name; rendered as the H1 title '# Playbook — <title>'. A short imperative phrase, e.g. 'Restore the Gradle wrapper'."`
	Intro    string    `json:"intro,omitempty" jsonschema:"optional lead prose before the first section (markdown)"`
	Sections []Section `json:"sections" jsonschema:"the ordered sections of the playbook; at least one"`
	Verify   *Step     `json:"verify,omitempty" jsonschema:"the final outcome-check command, rendered as the {id=verify} block. Include it for a troubleshooting/fix playbook."`
	Meta     Meta      `json:"meta" jsonschema:"classification + front-matter metadata for the saved playbook"`
}

// Section is a titled run of heterogeneous content.
type Section struct {
	Heading string        `json:"heading" jsonschema:"the section heading, rendered as '## <heading>'"`
	Content []ContentItem `json:"content" jsonschema:"ordered, heterogeneous list of prose and code items; render in order"`
}

// ContentItem is one prose or code element. Kind discriminates which fields apply
// (a flat discriminator, not a oneOf, for tool-use reliability).
type ContentItem struct {
	Kind     string   `json:"kind" jsonschema:"one of: text, callout, code"`
	Text     string   `json:"text,omitempty" jsonschema:"for kind=text or kind=callout: literate markdown prose (callout renders as a blockquote note)"`
	Lang     string   `json:"lang,omitempty" jsonschema:"for kind=code: the language/interpreter — bash|zsh|sh|python|diff|console|…"`
	Code     string   `json:"code,omitempty" jsonschema:"for kind=code: the block content"`
	ID       string   `json:"id,omitempty" jsonschema:"for kind=code: optional stable id for value-passing; we auto-assign when omitted"`
	Needs    []string `json:"needs,omitempty" jsonschema:"for kind=code: ids of earlier blocks this one depends on"`
	Rollback string   `json:"rollback,omitempty" jsonschema:"for kind=code: the id of the block this one rolls back"`
	Static   bool     `json:"static,omitempty" jsonschema:"for kind=code: true if the block is non-runnable (console output / illustrative)"`
}

// Step is a single command used for the top-level verify.
type Step struct {
	Lang  string   `json:"lang" jsonschema:"the language/interpreter for the verify command"`
	Code  string   `json:"code" jsonschema:"the verify command content"`
	Needs []string `json:"needs,omitempty" jsonschema:"ids the verify depends on (usually the fix block)"`
}

// Meta carries the classification + provenance fields folded into the same call
// (replacing the separate metadata model pass). The playbook NAME is the Title
// (the H1), so Meta has no name field.
type Meta struct {
	Description  string   `json:"description" jsonschema:"a one-line imperative summary of what the playbook does"`
	Category     string   `json:"category,omitempty" jsonschema:"a coarse category, e.g. 'Android / build' or 'macOS / networking'"`
	Tags         []string `json:"tags,omitempty" jsonschema:"keywords for search"`
	ProjectBound bool     `json:"project_bound" jsonschema:"true if this playbook is specific to a project/working directory; false for a general how-to that applies anywhere"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/playbook/ -run TestPlaybook_JSONRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/schema.go internal/playbook/schema_test.go
git commit -m "feat(playbook): Hybrid playbook schema (title/sections/content[]/verify/meta)"
```

---

### Task 2: Deterministic markdown renderer

**Files:**
- Create: `internal/playbook/render.go`
- Test: `internal/playbook/render_test.go`

**Interfaces:**
- Consumes: the Task 1 types.
- Produces: `func playbook.Render(pb Playbook) string` — the markdown BODY (no front matter). Used by Task 8.

- [ ] **Step 1: Write the failing tests**

```go
package playbook

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/ui"
)

func sample() Playbook {
	return Playbook{
		Title: "Restore the Gradle wrapper",
		Intro: "You ran `gg build` and it failed.",
		Sections: []Section{
			{Heading: "Goal & error", Content: []ContentItem{
				{Kind: "text", Text: "The wrapper jar is missing."},
				{Kind: "code", Lang: "console", Code: "Error: GradleWrapperMain", Static: true},
				{Kind: "callout", Text: "This directory is a git repo."},
			}},
			{Heading: "Fix", Content: []ContentItem{
				{Kind: "text", Text: "Restore the jar:"},
				{Kind: "code", Lang: "bash", Code: "gradle wrapper", ID: "fix"},
				{Kind: "text", Text: "Now the build works."},
			}},
		},
		Verify: &Step{Lang: "bash", Code: "gg build", Needs: []string{"fix"}},
		Meta:   Meta{Description: "Restore the wrapper", ProjectBound: true},
	}
}

func TestRender_Golden(t *testing.T) {
	got := Render(sample())
	want := "# Playbook — Restore the Gradle wrapper\n" +
		"\nYou ran `gg build` and it failed.\n" +
		"\n## Goal & error\n" +
		"\nThe wrapper jar is missing.\n" +
		"\n```console {static}\nError: GradleWrapperMain\n```\n" +
		"\n> This directory is a git repo.\n" +
		"\n## Fix\n" +
		"\nRestore the jar:\n" +
		"\n```bash {id=fix}\ngradle wrapper\n```\n" +
		"\nNow the build works.\n" +
		"\n```bash {id=verify needs=fix}\ngg build\n```\n"
	if got != want {
		t.Fatalf("render mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// The rendered body MUST be a valid playbook the viewer accepts.
func TestRender_IsValidPlaybook(t *testing.T) {
	if !ui.ValidatePlaybook(Render(sample())) {
		t.Fatalf("rendered playbook failed ui.ValidatePlaybook:\n%s", Render(sample()))
	}
}

// Code items with no id get a deterministic auto id; static items get no id.
func TestRender_AutoIDsAndStatic(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "a"},
		{Kind: "code", Lang: "console", Code: "out", Static: true},
		{Kind: "code", Lang: "bash", Code: "b"},
	}}}}
	got := Render(pb)
	if !strings.Contains(got, "```bash {id=step-1}\na\n```") {
		t.Errorf("first auto id wrong:\n%s", got)
	}
	if !strings.Contains(got, "```console {static}\nout\n```") {
		t.Errorf("static block should carry only {static}:\n%s", got)
	}
	if !strings.Contains(got, "```bash {id=step-2}\nb\n```") {
		t.Errorf("auto id must skip the static block:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/playbook/ -run TestRender`
Expected: FAIL — `Render` undefined.

- [ ] **Step 3: Implement the renderer**

```go
package playbook

import (
	"fmt"
	"strings"
)

// Render turns a structured Playbook into the canonical markdown BODY (no front
// matter — that is assembled at save from Meta). The output satisfies
// ui.ValidatePlaybook: an H1 "# Playbook — <Title>" + ≥1 fenced block. Blank
// lines separate every element so goldmark parses each as its own block.
func Render(pb Playbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Playbook — %s\n", pb.Title)
	writeProse(&b, pb.Intro)

	auto := 0
	for _, sec := range pb.Sections {
		fmt.Fprintf(&b, "\n## %s\n", sec.Heading)
		for _, it := range sec.Content {
			switch it.Kind {
			case "text":
				writeProse(&b, it.Text)
			case "callout":
				if s := strings.TrimSpace(it.Text); s != "" {
					b.WriteString("\n")
					b.WriteString(blockquote(s))
				}
			case "code":
				id := ""
				if !it.Static {
					if it.ID != "" {
						id = it.ID
					} else {
						auto++
						id = fmt.Sprintf("step-%d", auto)
					}
				}
				b.WriteString("\n")
				b.WriteString(fence(it.Lang, id, it.Needs, it.Rollback, it.Static, it.Code))
			}
		}
	}
	if pb.Verify != nil {
		b.WriteString("\n")
		b.WriteString(fence(pb.Verify.Lang, "verify", pb.Verify.Needs, "", false, pb.Verify.Code))
	}
	return b.String()
}

// writeProse appends a blank line + the trimmed prose + a newline (nothing when empty).
func writeProse(b *strings.Builder, s string) {
	if t := strings.TrimSpace(s); t != "" {
		b.WriteString("\n")
		b.WriteString(t)
		b.WriteString("\n")
	}
}

// fence renders one fenced code block with the {…} tag parseFenceInfo expects.
// Static blocks carry only {static}; runnable blocks carry {id=… needs=… rollback=…}.
func fence(lang, id string, needs []string, rollback string, static bool, code string) string {
	var tag string
	if static {
		tag = "{static}"
	} else {
		tag = "{id=" + id
		if len(needs) > 0 {
			tag += " needs=" + strings.Join(needs, ",")
		}
		if rollback != "" {
			tag += " rollback=" + rollback
		}
		tag += "}"
	}
	return "```" + lang + " " + tag + "\n" + strings.TrimRight(code, "\n") + "\n```\n"
}

// blockquote prefixes each line of a callout with "> ".
func blockquote(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n") + "\n"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/playbook/`
Expected: PASS (all three render tests + the Task 1 test).

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/render.go internal/playbook/render_test.go
git commit -m "feat(playbook): deterministic struct→markdown renderer (body only)"
```

---

### Task 3: Semantic validation

**Files:**
- Create: `internal/playbook/validate.go`
- Test: `internal/playbook/validate_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `func playbook.Validate(pb Playbook, requireVerify bool) error` — nil when valid, else a single error joining all violations. Used by Task 4 (the backend returns it as the tool error so Claude re-submits).

- [ ] **Step 1: Write the failing tests**

```go
package playbook

import (
	"strings"
	"testing"
)

func TestValidate_OK(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo a", ID: "fix"},
	}}}, Verify: &Step{Lang: "bash", Code: "ok"}}
	if err := Validate(pb, true); err != nil {
		t.Fatalf("want valid, got %v", err)
	}
}

func TestValidate_Violations(t *testing.T) {
	cases := []struct {
		name string
		pb   Playbook
		req  bool
		want string
	}{
		{"no title", Playbook{Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "a"}}}}}, false, "title"},
		{"no runnable block", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "console", Code: "x", Static: true}}}}}, false, "runnable"},
		{"dup id", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "x", ID: "fix"},
			{Kind: "code", Lang: "bash", Code: "y", ID: "fix"},
		}}}}, false, "duplicate id"},
		{"missing verify", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "fix"}}}}}, true, "verify"},
		{"bad kind", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "bogus"}, {Kind: "code", Lang: "bash", Code: "x", ID: "a"}}}}}, false, "kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.pb, c.req)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/playbook/ -run TestValidate`
Expected: FAIL — `Validate` undefined.

- [ ] **Step 3: Implement validation**

```go
package playbook

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks the semantic rules the JSON schema cannot express. It returns
// nil when valid, else one error joining every violation (so a re-submitting
// model sees all problems at once). requireVerify demands a top-level Verify
// (set for a troubleshooting/fix playbook; create passes false).
func Validate(pb Playbook, requireVerify bool) error {
	var errs []string
	if strings.TrimSpace(pb.Title) == "" {
		errs = append(errs, "title is required")
	}
	runnable := 0
	seen := map[string]bool{}
	for si, sec := range pb.Sections {
		for ci, it := range sec.Content {
			switch it.Kind {
			case "text", "callout":
				// prose: nothing structural to check
			case "code":
				if !it.Static {
					runnable++
					if it.ID != "" {
						if seen[it.ID] {
							errs = append(errs, fmt.Sprintf("duplicate id %q", it.ID))
						}
						seen[it.ID] = true
					}
				}
			default:
				errs = append(errs, fmt.Sprintf("section %d content %d: unknown kind %q (want text|callout|code)", si, ci, it.Kind))
			}
		}
	}
	if runnable == 0 {
		errs = append(errs, "at least one runnable (non-static) code block is required")
	}
	if requireVerify && pb.Verify == nil {
		errs = append(errs, "a top-level verify command is required for this playbook")
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New("invalid playbook: " + strings.Join(errs, "; "))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/playbook/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/playbook/validate.go internal/playbook/validate_test.go
git commit -m "feat(playbook): semantic validation (title, runnable block, unique ids, verify)"
```

---

### Task 4: `submit_playbook` in the tools backend

**Files:**
- Modify: `internal/tools/tools.go` (the `request`/`reply` structs, `dispatch`, add `doSubmitPlaybook`; `Deps`)
- Modify: `internal/tools/client.go` (the `Call`/`Result` structs)
- Test: `internal/tools/tools_test.go` (add cases)

**Interfaces:**
- Consumes: `playbook.Playbook`, `playbook.Validate` (Task 1/3).
- Produces: a new tool `"submit_playbook"`; `Deps.OnPlaybook func(playbook.Playbook)`; the `request`/`Call` field `Playbook json.RawMessage` (key `"playbook"`); on a valid submit the backend calls `OnPlaybook` and replies `{ok:true}`; on an invalid submit it replies `{error: "<validation>"}` and does NOT call `OnPlaybook`. Task 5 forwards to it; Task 8 sets `OnPlaybook`.

- [ ] **Step 1: Write the failing test** (append to `internal/tools/tools_test.go`)

```go
func TestServe_SubmitPlaybook(t *testing.T) {
	d := newTestDriver(t)
	var got playbook.Playbook
	gotN := 0
	socket := serveTest(t, Deps{Driver: d, OnPlaybook: func(pb playbook.Playbook) { got = pb; gotN++ }})

	pb := playbook.Playbook{
		Title:    "T",
		Sections: []playbook.Section{{Heading: "S", Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", Code: "echo hi", ID: "fix"}}}},
		Meta:     playbook.Meta{Description: "d", ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)

	res, err := Dial(socket, Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("Dial submit: %v", err)
	}
	if !res.OK || res.Error != "" {
		t.Fatalf("submit reply = %+v, want ok", res)
	}
	if gotN != 1 || got.Title != "T" || !got.Meta.ProjectBound {
		t.Fatalf("OnPlaybook got %d calls, pb=%+v", gotN, got)
	}

	// An invalid playbook (no runnable block) is rejected and NOT delivered.
	bad, _ := json.Marshal(playbook.Playbook{Title: "T", Sections: []playbook.Section{{Heading: "S"}}})
	res, err = Dial(socket, Call{Tool: "submit_playbook", Playbook: bad})
	if err != nil {
		t.Fatalf("Dial bad submit: %v", err)
	}
	if res.OK || res.Error == "" {
		t.Fatalf("bad submit should be rejected, got %+v", res)
	}
	if gotN != 1 {
		t.Fatalf("invalid submit must not call OnPlaybook (calls=%d)", gotN)
	}
}
```

Add the import `"github.com/Townk/ai-playbook/internal/playbook"` and `"encoding/json"` to `tools_test.go` if absent.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestServe_SubmitPlaybook`
Expected: FAIL — `Deps.OnPlaybook` / `Call.Playbook` / tool unknown.

- [ ] **Step 3: Implement the backend tool**

In `internal/tools/tools.go`, add to `Deps` (after `Ask`):
```go
	// OnPlaybook, when set, receives a validated structured playbook submitted via
	// the submit_playbook tool. nil → submit_playbook replies "unavailable".
	OnPlaybook func(pb playbook.Playbook)
```
Add to the `request` struct:
```go
	Playbook json.RawMessage `json:"playbook,omitempty"` // submit_playbook: the structured playbook
```
Add to the `reply` struct (if no `OK` field exists for this use, the existing `OK bool` is reused). Add the dispatch case in `dispatch`:
```go
	case "submit_playbook":
		return s.doSubmitPlaybook(req)
```
Add the handler:
```go
// doSubmitPlaybook decodes a structured playbook, validates it, and (on success)
// hands it to Deps.OnPlaybook. A validation failure is returned as reply.Error so
// the MCP adapter surfaces it as a tool error and the model re-submits.
func (s *Server) doSubmitPlaybook(req request) reply {
	if s.deps.OnPlaybook == nil {
		return reply{Error: "submit_playbook unavailable in this context"}
	}
	var pb playbook.Playbook
	if err := json.Unmarshal(req.Playbook, &pb); err != nil {
		return reply{Error: "could not parse playbook: " + err.Error()}
	}
	if err := playbook.Validate(pb, false); err != nil {
		return reply{Error: err.Error()}
	}
	s.deps.OnPlaybook(pb)
	return reply{OK: true}
}
```
Add imports to `tools.go`: `"encoding/json"` (if absent) and `"github.com/Townk/ai-playbook/internal/playbook"`.

In `internal/tools/client.go`, add to `Call`:
```go
	Playbook json.RawMessage `json:"playbook,omitempty"`
```
(The `Result` already has `OK bool` and `Error string`.) Add `"encoding/json"` to `client.go` imports if absent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tools/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/tools.go internal/tools/client.go internal/tools/tools_test.go
git commit -m "feat(tools): submit_playbook backend tool + Deps.OnPlaybook capture"
```

---

### Task 5: `submit_playbook` MCP tool (schema = the playbook)

**Files:**
- Modify: `internal/mcpserver/mcpserver.go` (register the tool + handler)
- Test: `internal/mcpserver/mcpserver_test.go` (add a forward test + a schema-shape test)

**Interfaces:**
- Consumes: `playbook.Playbook` (the tool's input type → its JSON schema), `tools.Call{Tool:"submit_playbook", Playbook: <json>}` (Task 4).
- Produces: an MCP tool `submit_playbook` whose `inputSchema` is generated from `playbook.Playbook`; its handler marshals the typed input and forwards it to the backend, returning the backend's `ok`/error as the tool result.

- [ ] **Step 1: Write the failing tests** (append to `internal/mcpserver/mcpserver_test.go`)

```go
func TestForward_SubmitPlaybook(t *testing.T) {
	fb := startFakeBackend(t, tools.Result{OK: true})

	pb := playbook.Playbook{Title: "T", Sections: []playbook.Section{{Heading: "S",
		Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "fix"}}}}}
	raw, _ := json.Marshal(pb)
	res, err := forward(fb.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if res.IsError {
		t.Errorf("healthy submit should not be IsError: %+v", res)
	}
	got := fb.lastCall()
	if got.Tool != "submit_playbook" || len(got.Playbook) == 0 {
		t.Errorf("backend got %+v, want submit_playbook with payload", got)
	}
	if txt := contentText(t, res); !strings.Contains(txt, "saved") {
		t.Errorf("ok submit result = %q, want 'saved'", txt)
	}
}

// The submit_playbook tool's input schema is generated from playbook.Playbook —
// it must expose the playbook fields (a guard that the schema IS the playbook).
func TestSubmitPlaybook_SchemaShape(t *testing.T) {
	srv := newServer("/tmp/unused.sock")
	tools, err := srv.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var sp *mcp.Tool
	for _, tl := range tools.Tools {
		if tl.Name == "submit_playbook" {
			sp = tl
		}
	}
	if sp == nil {
		t.Fatal("submit_playbook tool not registered")
	}
	schema, _ := json.Marshal(sp.InputSchema)
	for _, want := range []string{"title", "sections", "verify", "project_bound"} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("submit_playbook input schema missing %q:\n%s", want, schema)
		}
	}
}
```

Add imports to the test file as needed: `"context"`, `"encoding/json"`, the mcp package alias already used by mcpserver (check the file's import — it is `"github.com/modelcontextprotocol/go-sdk/mcp"` as `mcp`), and `"github.com/Townk/ai-playbook/internal/playbook"`.

> If `srv.ListTools`/`mcp.ListToolsParams` is not the exact accessor in v1.6.1, replace the schema-shape test body with: marshal the result of the registered tool's generated schema by calling the same schema generator `mcp.AddTool` uses. Verify the available API first with: `go doc github.com/modelcontextprotocol/go-sdk/mcp.Server` and `go doc github.com/modelcontextprotocol/go-sdk/mcp.AddTool`. The forward test (TestForward_SubmitPlaybook) does NOT depend on this and must pass regardless.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver/ -run 'SubmitPlaybook'`
Expected: FAIL — tool not registered.

- [ ] **Step 3: Register the tool + handler**

In `internal/mcpserver/mcpserver.go`, inside `newServer`, after the `ask` registration:
```go
	mcp.AddTool(server, &mcp.Tool{
		Name:        "submit_playbook",
		Description: "Submit the FINISHED playbook as structured data. This is your FINAL action and your deliverable — do NOT write the playbook as markdown in your reply; call this tool with the playbook object instead. The host renders the markdown. If it returns a validation error, fix the object and call submit_playbook again.",
	}, submitPlaybookHandler(socketPath))
```
Add the handler (the input type IS `playbook.Playbook`, so the SDK generates the schema from it):
```go
func submitPlaybookHandler(socketPath string) mcp.ToolHandlerFor[playbook.Playbook, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in playbook.Playbook) (*mcp.CallToolResult, any, error) {
		raw, err := json.Marshal(in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "could not encode playbook: " + err.Error()}}}, nil, nil
		}
		r, ferr := forward(socketPath, tools.Call{Tool: "submit_playbook", Playbook: raw})
		return r, nil, ferr
	}
}
```
Add a `renderResult` case so an OK submit reads cleanly (in `renderResult`'s switch):
```go
	case "submit_playbook":
		if res.Error != "" {
			return "validation error: " + res.Error
		}
		if res.OK {
			return "saved"
		}
		return "not saved"
```
Add imports to `mcpserver.go`: `"encoding/json"` (if absent) and `"github.com/Townk/ai-playbook/internal/playbook"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/`
Expected: PASS. (If the schema-shape test hit the API caveat, confirm `go doc` and adjust per the Step-1 note; the forward test must pass.)

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/mcpserver.go internal/mcpserver/mcpserver_test.go
git commit -m "feat(mcpserver): submit_playbook MCP tool (input schema = the playbook)"
```

---

### Task 6: Structured authoring prompt

**Files:**
- Create: `internal/author/structured.go`
- Test: `internal/author/structured_test.go`

**Interfaces:**
- Produces: `func author.StructuredToolInstruction() string` — the system-prompt addendum telling the model to diagnose with `run`/`ask`, then call `submit_playbook` (NOT write markdown), and how the schema maps (title, sections/content kinds, verify, meta incl. project_bound). Used by Task 8 in place of `ToolInstruction` for the create path.

- [ ] **Step 1: Write the failing test**

```go
package author

import (
	"strings"
	"testing"
)

func TestStructuredToolInstruction_MandatesSubmit(t *testing.T) {
	s := StructuredToolInstruction()
	for _, want := range []string{
		"submit_playbook", // names the tool
		"project_bound",   // explains the gating bool
		"do NOT write",    // forbids markdown output
		"callout",         // explains content kinds
		"verify",          // explains the verify field
	} {
		if !strings.Contains(s, want) {
			t.Errorf("structured instruction missing %q", want)
		}
	}
	// It must NOT instruct the old "{id=fix}/{id=verify} fenced code blocks" markdown.
	if strings.Contains(s, "fenced code blocks") {
		t.Errorf("structured instruction must not ask for markdown fenced blocks")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/author/ -run TestStructuredToolInstruction`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the instruction**

```go
package author

// StructuredToolInstruction is the system-prompt addendum for the STRUCTURED
// authoring path (the create flow). Unlike ToolInstruction (which asks the model
// to write `{id=fix}` markdown), it directs the model to diagnose with run/ask
// and then return the playbook as DATA via the submit_playbook tool. The host
// renders the markdown, so the model never formats it.
func StructuredToolInstruction() string {
	return "\n\n" +
		"## Diagnosing and submitting the playbook\n" +
		"You have MCP tools `run`, `remember`, `ask`, and `submit_playbook`.\n" +
		"- Use `run` ONLY to DIAGNOSE (reproduce the situation, inspect cwd/files/versions). " +
		"It runs in the USER's real shell — keep it READ-ONLY; do NOT apply changes with it.\n" +
		"- Use `ask` to get input from the user and `remember` for a durable project fact.\n" +
		"- Your DELIVERABLE is a single `submit_playbook` call. Do NOT write the playbook as " +
		"markdown in your reply — call `submit_playbook` with the structured object. The host " +
		"renders the markdown deterministically.\n" +
		"\n" +
		"### The playbook object\n" +
		"- `title`: a short imperative name (rendered as the H1).\n" +
		"- `intro`: optional lead prose.\n" +
		"- `sections[]`: each has a `heading` and an ordered `content[]`. Each content item is one of:\n" +
		"  - `{kind:\"text\", text}` — literate prose (markdown).\n" +
		"  - `{kind:\"callout\", text}` — a note/warning (rendered as a blockquote).\n" +
		"  - `{kind:\"code\", lang, code, id?, needs?, rollback?, static?}` — a block. Mark non-runnable " +
		"output (console transcripts, illustrations) `static:true`. Use `id`+`needs` for value-passing " +
		"between runnable blocks (omit `id` to have one assigned).\n" +
		"- `verify`: the final outcome-check command — include it for a fix/troubleshooting playbook.\n" +
		"- `meta`: `description` (one line), `category`, `tags`, and `project_bound`. Set " +
		"`project_bound:true` when the playbook is specific to a project/working directory; `false` for " +
		"a general how-to that applies anywhere.\n" +
		"Interleave prose and code freely inside a section (intro prose, a block, closing prose).\n"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/author/ -run TestStructuredToolInstruction`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/author/structured.go internal/author/structured_test.go
git commit -m "feat(author): structured authoring instruction (submit_playbook, no markdown)"
```

---

### Task 7: `project_bound` through the front matter + create's metadata fold-in seam

**Files:**
- Modify: `internal/frontmatter/frontmatter.go` (add `ProjectBound` to `FrontMatter`)
- Modify: `internal/orchestrator/orchestrator.go` (add `ProjectBound` to `PlaybookMeta`; set it in `buildFrontMatter`)
- Test: `internal/frontmatter/frontmatter_test.go`, `internal/orchestrator/orchestrator_test.go` (add cases)

**Interfaces:**
- Consumes: nothing new.
- Produces: `frontmatter.FrontMatter.ProjectBound bool` (yaml `project_bound`); `orchestrator.PlaybookMeta.ProjectBound bool`; `buildFrontMatter` copies `meta.ProjectBound → fm.ProjectBound`. Task 8 injects a `Metadata` seam returning a `PlaybookMeta` with `ProjectBound` set from the captured schema meta.

- [ ] **Step 1: Write the failing tests**

In `internal/frontmatter/frontmatter_test.go`:
```go
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
```
In `internal/orchestrator/orchestrator_test.go` (use the existing Reengage test pattern; a fake Metadata seam returning ProjectBound):
```go
func TestBuildFrontMatter_ProjectBound(t *testing.T) {
	re := &Reengage{
		Req:      capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) { return PlaybookMeta{Description: "d", ProjectBound: true}, nil },
	}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\nx\n```\n")
	if !fm.ProjectBound {
		t.Fatalf("buildFrontMatter must copy ProjectBound from the seam meta")
	}
	if fm.Description != "d" {
		t.Fatalf("description = %q, want d", fm.Description)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/frontmatter/ ./internal/orchestrator/ -run 'ProjectBound'`
Expected: FAIL — `ProjectBound` undefined.

- [ ] **Step 3: Implement**

In `internal/frontmatter/frontmatter.go`, add to the `FrontMatter` struct (after `Workdir`):
```go
	ProjectBound bool `yaml:"project_bound,omitempty" json:"project_bound,omitempty"`
```
In `internal/orchestrator/orchestrator.go`, add to `PlaybookMeta` (find its definition near `Reengage`):
```go
	ProjectBound bool
```
In `buildFrontMatter`, after fetching `meta` from the seam, include it in the returned `FrontMatter`:
```go
		ProjectBound: meta.ProjectBound,
```
(Add `ProjectBound: meta.ProjectBound,` to the `return frontmatter.FrontMatter{…}` literal.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/frontmatter/ ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/frontmatter/frontmatter.go internal/frontmatter/frontmatter_test.go internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(frontmatter,orchestrator): project_bound front-matter field"
```

---

### Task 8: Wire `create` to structured authoring

**Files:**
- Modify: `internal/launcher/session.go` (`session` struct + `openSession` set `Deps.OnPlaybook`; a captured-meta seam helper)
- Modify: `internal/launcher/create_progress.go` (`realCreateStream` uses the structured prompt + captures; `body()` returns `playbook.Render`; `newCreateReengage` injects the captured-meta seam)
- Test: `internal/launcher/create_progress_test.go` (end-to-end with a fake backend submit)

**Interfaces:**
- Consumes: `playbook.Render` (Task 2), `Deps.OnPlaybook` (Task 4), `author.StructuredToolInstruction` (Task 6), `PlaybookMeta.ProjectBound` (Task 7).
- Produces: a `create` flow where the rendered body comes from the submitted structured playbook and the save seam returns the captured meta (no metadata model pass).

**Design notes (read before implementing):**
- `openSession` (session.go) builds `tools.Deps`. Add a capture: a `session.pb` field (`chan playbook.Playbook`, buffered 1) and set `Deps.OnPlaybook` to send the captured playbook onto it. The authoring agent calls `submit_playbook` → backend → `OnPlaybook` → `session.pb`.
- The authoring system prompt for create must append `author.StructuredToolInstruction()` instead of `ToolInstruction`. Today the addendum is appended inside `RunHarnessEvents` when `MCPConfigPath != ""` (it appends `ToolInstruction`). Add an `AuthorOptions.Structured bool`; when set, `RunHarnessEvents` appends `StructuredToolInstruction()` instead of `ToolInstruction`. `realCreateStream` sets `Structured: true`.
- `realCreateStream`'s `body()` must, after the stream drains, return `playbook.Render(<captured pb>)`. Capture: read `sess.pb` non-blocking after EOF; if nothing was submitted (model misbehaved), fall back to the accumulated text (existing behavior) so create never dead-ends.
- `newCreateReengage` injects `Metadata` = a closure returning the captured pb's meta as `orchestrator.PlaybookMeta{Description, Category, Tags, ProjectBound, EnvNotes: nil}` — NO `author.PlaybookMetadata` model call. The captured pb is shared from `realCreateStream` (store it on the session, e.g. `session.lastPB *playbook.Playbook`, set in the `OnPlaybook` callback).

- [ ] **Step 1: Write the failing test** (`internal/launcher/create_progress_test.go`)

```go
// create authors via submit_playbook: a fake backend delivers a structured
// playbook; the create stream's body is the DETERMINISTIC render of it, and the
// reengage Metadata seam returns the captured meta (no model pass).
func TestCreate_StructuredRenderAndSeam(t *testing.T) {
	sess := newFakeSession(t) // helper: openSession-like with a real tools.Server + driver
	pb := playbook.Playbook{
		Title:    "Restore wrapper",
		Sections: []playbook.Section{{Heading: "Fix", Content: []playbook.ContentItem{{Kind: "code", Lang: "bash", Code: "gradle wrapper", ID: "fix"}}}},
		Meta:     playbook.Meta{Description: "Restore the wrapper", Category: "Android / build", Tags: []string{"gradle"}, ProjectBound: true},
	}
	raw, _ := json.Marshal(pb)
	// Simulate the agent's tool call hitting the backend.
	res, err := tools.Dial(sess.socket, tools.Call{Tool: "submit_playbook", Playbook: raw})
	if err != nil || !res.OK {
		t.Fatalf("submit: %+v err=%v", res, err)
	}

	// The captured playbook renders deterministically.
	body := playbook.Render(*sess.lastPB)
	if !strings.Contains(body, "# Playbook — Restore wrapper") || !strings.Contains(body, "```bash {id=fix}") {
		t.Fatalf("rendered body wrong:\n%s", body)
	}

	// The create reengage Metadata seam returns the captured meta — no model call.
	re := newCreateReengage(capture.Request{}, triage.Decision{Disabled: true}, nil, true, sess, config.Default())
	meta, err := re.Metadata(body)
	if err != nil {
		t.Fatalf("seam err: %v", err)
	}
	if meta.Description != "Restore the wrapper" || !meta.ProjectBound || meta.Category != "Android / build" {
		t.Fatalf("seam meta = %+v, want captured schema meta", meta)
	}
}
```

> The `newFakeSession` helper builds a `session` with a real `tools.Serve` whose `Deps.OnPlaybook` stores into `session.lastPB` (and `session.pb`), mirroring `openSession`. Put it next to the existing create-path test helpers; reuse `newTestDriver` from the driver test pattern via a small exported test seam or an in-package constructor. If `openSession` can be called directly in-package with a `mux.Null()` and a temp cwd, prefer that over a bespoke helper.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run TestCreate_StructuredRenderAndSeam`
Expected: FAIL — `session.lastPB` / structured seam not wired.

- [ ] **Step 3: Implement the wiring**

1. `internal/launcher/session.go` — add to the `session` struct:
```go
	pb     chan playbook.Playbook // submit_playbook capture (buffered 1)
	lastPB *playbook.Playbook     // the most recent captured playbook (for the meta seam)
```
In `openSession`, before `tools.Serve`, create the channel and the callback:
```go
	pbCh := make(chan playbook.Playbook, 1)
	sess := &session{ /* existing fields */ }
	onPlaybook := func(p playbook.Playbook) {
		sess.lastPB = &p
		select {
		case pbCh <- p:
		default:
		}
	}
```
Wire `OnPlaybook: onPlaybook` into the `tools.Deps{…}` literal and set `sess.pb = pbCh`. (Restructure the existing `return &session{…}` so the struct is built before `tools.Serve` and the callback closes over it.)

2. `internal/author/events.go` — add `Structured bool` to `AuthorOptions`; in `RunHarnessEvents`, where it currently does `if opts.MCPConfigPath != "" { sys += ToolInstruction }`, change to:
```go
		if opts.MCPConfigPath != "" {
			if opts.Structured {
				sys += StructuredToolInstruction()
			} else {
				sys += ToolInstruction
			}
		}
```

3. `internal/launcher/create_progress.go` — in `realCreateStream`, pass `Structured: true` in the `author.AuthorOptions{…}` and make `body()` prefer the captured render:
```go
	body := func() string {
		if sess != nil && sess.lastPB != nil {
			return playbook.Render(*sess.lastPB)
		}
		return acc.String() // existing text accumulator fallback
	}
```
In `newCreateReengage`, replace `Metadata: buildMetadataSeam(sess)` with a captured-meta seam:
```go
		Metadata: capturedMetaSeam(sess),
```
Add the helper (in create_progress.go):
```go
// capturedMetaSeam returns the structured playbook's meta as the front-matter
// classification — NO metadata model pass. Falls back to the model classifier
// only if create produced no structured playbook (text fallback path).
func capturedMetaSeam(sess *session) func(doc string) (orchestrator.PlaybookMeta, error) {
	return func(doc string) (orchestrator.PlaybookMeta, error) {
		if sess != nil && sess.lastPB != nil {
			m := sess.lastPB.Meta
			return orchestrator.PlaybookMeta{
				Description:  m.Description,
				Category:     m.Category,
				Tags:         m.Tags,
				ProjectBound: m.ProjectBound,
			}, nil
		}
		return buildMetadataSeam(sess)(doc)
	}
}
```
Add imports (`internal/playbook`) where used.

- [ ] **Step 4: Run the test + the full suite**

Run: `go test ./internal/launcher/ -run TestCreate_StructuredRenderAndSeam`
Expected: PASS.
Run: `go build ./... && go test ./...`
Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/session.go internal/launcher/create_progress.go internal/author/events.go internal/launcher/create_progress_test.go
git commit -m "feat(create): author via submit_playbook → render structured playbook (metadata folded in)"
```

---

## Self-Review

**Spec coverage** (against `docs/specifications/structured-playbook-output.md`):
- `submit_playbook` tool-use, schema = playbook → Tasks 4 + 5. ✓
- Hybrid schema (content[] kind=text|callout|code, top-level verify, meta) → Task 1. ✓
- Deterministic renderer (matches the block-tag format) → Task 2. ✓
- Schema + semantic validation with re-ask → Task 3 (semantic) + Task 4 (returned as tool error → Claude re-submits; schema-level by Claude's tool-use). ✓
- Metadata folded in / single pass → Task 8 (captured-meta seam; no `PlaybookMetadata` model call for create). ✓
- `project_bound` written to front matter → Task 7. ✓
- Collect-then-render (no streaming) → preserved: create already drains to EOF; the structured body is rendered after EOF (Task 8). ✓
- Phase A = create only; escalate/adapt/regenerate untouched → Global Constraints + Task 8 scope. ✓
- Prove prose quality → the create end-to-end (Task 8) + manual eyeball after install (call out in handoff). ✓

**Deferred to Phase B (intentionally not in this plan):** removing `workdir`; gating adapt-on-run on `project_bound`; migrating escalate/adapt/regenerate/followup; the structured polish/generalize pass (only if quality requires it).

**Open decision to confirm with the user at handoff:** the schema dedup (top-level `title` is the name; `meta` drops `name`) diverges from the spec's `meta` list — the spec showed both `title` and `meta.name`. The plan removes the redundant `meta.name`. If the user wants a distinct `meta.name`, Task 1 + Task 8's seam adjust trivially.

**Type consistency:** `playbook.Playbook/Section/ContentItem/Step/Meta` field names are identical across Tasks 1, 2, 3, 4, 5, 8. `Deps.OnPlaybook` (Task 4) ↔ `openSession` setter (Task 8). `PlaybookMeta.ProjectBound` (Task 7) ↔ `capturedMetaSeam` (Task 8). `AuthorOptions.Structured` (Task 8 step 3) ↔ `realCreateStream` setter (Task 8). ✓

**Placeholder scan:** none — every code step has complete code. The one API-uncertainty (the SDK schema-introspection accessor in Task 5's schema-shape test) carries an explicit `go doc` verification step and a fallback, and does not gate the feature.
