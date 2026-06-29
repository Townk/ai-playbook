# Structured Playbook Phase B2b — Implementation Plan (pre-run variable confirmation gate)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before the first block of a stored playbook executes, confirm its declared variables with the user (Confirm/Customize), export the final values into the run driver, then run the requested block — built as a reusable gate the future assisted-run reuses.

**Architecture:** A read-first viewer renders the playbook; the first `run ▶` on an `env`-bearing playbook invokes a confirmation gate that drives a sequence of overlay dialogs (one confirm per balanced ≤5 group, optional per-var line edits), shell-exports the final values via the driver's main shell, marks itself satisfied (once per session), then fires the deferred block. The gate uses the model's existing ask-overlay machinery (`m.ask`/`askMode`/`askCompletion`) for BOTH mux and no-mux — the mux text-float can't render confirms.

**Tech Stack:** Go; bubbletea v2 viewer (`internal/ui`); `internal/input` ask dialogs; `internal/driver`; `internal/frontmatter`.

## Global Constraints

- Module `github.com/Townk/ai-playbook`. gpg-signed Conventional Commits; NO `Co-Authored-By` trailers; `git add` explicit paths; verify signing `git log -1 --format=%G?` == `G`.
- The confirmation renders through the **in-viewer overlay** (`input.NewAsk`) for both mux and no-mux — NOT the mux text-float (`AskFunc`), which can't render confirm/choose. Only the overlay's `NewAsk` gets the new labels; no `floatinput` change.
- The gate fires on the **first block-run** of an `env`-bearing playbook, **once per session** (a `gateSatisfied` flag); empty `env` → never prompts. ESC at any dialog returns to reading (viewer open, block not run, nothing exported, gate stays unsatisfied).
- The gate logic must be **callable independently of the first-block-run trigger** so the Phase-2 assisted-run reuses it.
- Grouping is balanced, max 5: `groups = ceil(N/5)`, fill each at `size = ceil(N/groups)` (e.g. 6→[3,3], 11→[4,4,3], 12→[4,4,4], 13→[5,5,3], 16→[4,4,4,4]); `N==0` → no groups.
- Values: `PROJECT_ROOT` → the heuristic root; every other var → its live shell value (`os.Getenv`, empty if unset). Final values are shell-quoted on export.
- `gofmt -l` clean; `go vet` clean; touched packages pass `go test` (and `-race` for `internal/ui`/`internal/driver`).
- Reuse: B1 ask machinery (`m.ask`/`askMode`/`askCompletion`, `input.NewAsk`); B2a `ui.SetProjectRoot`/`pendingProjectRoot`.

---

### Task 1: Balanced grouping helper

**Files:**
- Create: `internal/ui/confirm_gate.go`
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Produces: `func groupSizes(n int) []int` — per-dialog sizes; consumed by Task 6.

- [ ] **Step 1: Write the failing test**

```go
package ui

import (
	"reflect"
	"testing"
)

func TestGroupSizes(t *testing.T) {
	cases := []struct {
		n    int
		want []int
	}{
		{0, nil},
		{1, []int{1}},
		{5, []int{5}},
		{6, []int{3, 3}},
		{11, []int{4, 4, 3}},
		{12, []int{4, 4, 4}},
		{13, []int{5, 5, 3}},
		{16, []int{4, 4, 4, 4}},
	}
	for _, c := range cases {
		if got := groupSizes(c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("groupSizes(%d) = %v, want %v", c.n, got, c.want)
		}
		// every group ≤ 5
		for _, s := range groupSizes(c.n) {
			if s > 5 || s < 1 {
				t.Errorf("groupSizes(%d) produced out-of-range size %d", c.n, s)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestGroupSizes`
Expected: FAIL — `groupSizes` undefined.

- [ ] **Step 3: Implement** (start `internal/ui/confirm_gate.go`)

```go
package ui

// groupSizes returns the per-dialog variable counts for n variables: ceil(n/5)
// balanced groups, each ≤5, filled at size ceil(n/groups). n<=0 → nil.
// e.g. 6→[3,3], 13→[5,5,3], 12→[4,4,4].
func groupSizes(n int) []int {
	if n <= 0 {
		return nil
	}
	groups := (n + 4) / 5         // ceil(n/5)
	size := (n + groups - 1) / groups // ceil(n/groups)
	var sizes []int
	for n > 0 {
		s := size
		if s > n {
			s = n
		}
		sizes = append(sizes, s)
		n -= s
	}
	return sizes
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestGroupSizes`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/confirm_gate.go internal/ui/confirm_gate_test.go
git commit -m "feat(ui): balanced grouping helper for the confirmation gate"
```

---

### Task 2: Variable-list builder

**Files:**
- Modify: `internal/ui/confirm_gate.go`
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Consumes: `frontmatter.EnvValue` (`{Value, Why string}`).
- Produces: `type confirmVar struct{ Name, Value, Why string }`; `func buildConfirmVars(env map[string]frontmatter.EnvValue, projectRoot string, getenv func(string) string) []confirmVar`. Consumed by Task 6.

- [ ] **Step 1: Write the failing test**

```go
func TestBuildConfirmVars(t *testing.T) {
	env := map[string]frontmatter.EnvValue{
		"PROJECT_ROOT":     {Why: "the project directory"},
		"ANDROID_SDK_ROOT": {Why: "the SDK"},
		"UNSET_VAR":        {Why: "not in shell"},
	}
	getenv := func(k string) string {
		if k == "ANDROID_SDK_ROOT" {
			return "/live/sdk"
		}
		return ""
	}
	got := buildConfirmVars(env, "/new/proj", getenv)
	want := []confirmVar{
		{"ANDROID_SDK_ROOT", "/live/sdk", "the SDK"},
		{"PROJECT_ROOT", "/new/proj", "the project directory"},
		{"UNSET_VAR", "", "not in shell"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildConfirmVars = %v, want %v", got, want)
	}
}
```
(Add `"github.com/Townk/ai-playbook/internal/frontmatter"` to the test imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestBuildConfirmVars`
Expected: FAIL — `confirmVar`/`buildConfirmVars` undefined.

- [ ] **Step 3: Implement** (append to `confirm_gate.go`)

```go
import (
	"sort"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// confirmVar is one variable shown in the confirmation gate.
type confirmVar struct {
	Name  string
	Value string
	Why   string
}

// buildConfirmVars builds the gate's variable list from the declared front-matter env,
// the heuristic project root, and a getenv func (injected for tests). PROJECT_ROOT takes
// the project root; every other var takes its live shell value (empty if unset). Sorted
// by name for stable dialog ordering.
func buildConfirmVars(env map[string]frontmatter.EnvValue, projectRoot string, getenv func(string) string) []confirmVar {
	out := make([]confirmVar, 0, len(env))
	for name, ev := range env {
		val := getenv(name)
		if name == "PROJECT_ROOT" {
			val = projectRoot
		}
		out = append(out, confirmVar{Name: name, Value: val, Why: ev.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
```
(Merge the `import` block with the file's existing imports.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestBuildConfirmVars`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/confirm_gate.go internal/ui/confirm_gate_test.go
git commit -m "feat(ui): confirmation-gate variable-list builder"
```

---

### Task 3: Custom confirm-button labels in `input.NewAsk`

**Files:**
- Modify: `internal/input/ask.go` (`NewAsk` signature + the `confirm` case)
- Modify: all `input.NewAsk(` callers to pass the two new args (search the repo)
- Test: `internal/input/ask_test.go`

**Interfaces:**
- Produces: `func NewAsk(title, prompt, value, typ string, choices []string, affLabel, negLabel string) *Ask` — for `typ=="confirm"`, `affLabel`/`negLabel` label the two buttons; `""` falls back to `"Yes"`/`"No"`.

**Context:** `NewAsk` (`internal/input/ask.go`) currently hardcodes `newConfirmField(theme, variant, "Yes", "No", false)`. `newConfirmField(theme, variant, affirmative, negative string, defaultNegative bool)` already accepts custom labels; this task just threads them through. The confirm field returns `"yes"`/`"no"` as its value regardless of label.

- [ ] **Step 1: Write the failing test**

```go
func TestNewAsk_ConfirmCustomLabels(t *testing.T) {
	a := NewAsk("t", "p", "", "confirm", nil, "Confirm", "Customize")
	v := a.View(60)
	if !strings.Contains(v, "Confirm") || !strings.Contains(v, "Customize") {
		t.Fatalf("confirm view missing custom labels:\n%s", v)
	}
	// empty labels fall back to Yes/No
	b := NewAsk("t", "p", "", "confirm", nil, "", "")
	if vb := b.View(60); !strings.Contains(vb, "Yes") || !strings.Contains(vb, "No") {
		t.Fatalf("confirm view missing default labels:\n%s", vb)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/input/ -run TestNewAsk_ConfirmCustomLabels`
Expected: FAIL — `NewAsk` takes 5 args, not 7.

- [ ] **Step 3: Implement**

In `internal/input/ask.go`, change the signature and the confirm case:
```go
func NewAsk(title, prompt, value, typ string, choices []string, affLabel, negLabel string) *Ask {
	...
	case "confirm":
		aff, neg := affLabel, negLabel
		if aff == "" {
			aff = "Yes"
		}
		if neg == "" {
			neg = "No"
		}
		m := newInputModel(theme, variant, title, prompt, "", "", 1, 1, 1, false, "")
		m.fld = newConfirmField(theme, variant, aff, neg, false)
	...
}
```
Then update every other `input.NewAsk(` caller (e.g. `internal/ui/ask_overlay.go` `openAsk`) to pass `"", ""` as the final two args. Find them: `grep -rn 'input.NewAsk(\|NewAsk(' internal/ --include='*.go'`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/input/ ./internal/ui/`
Expected: PASS (callers compile with the new args).

- [ ] **Step 5: Commit**

```bash
git add internal/input/ask.go internal/input/ask_test.go internal/ui/ask_overlay.go
git commit -m "feat(input): custom confirm-button labels in NewAsk"
```
(Add any other caller files you touched to the `git add`.)

---

### Task 4: Exported driver main-context command

**Files:**
- Modify: `internal/driver/driver.go` (export a `RunMain` wrapper)
- Test: `internal/driver/driver_test.go`

**Interfaces:**
- Produces: `func (d *Driver) RunMain(cmd string, timeout time.Duration)` — runs `cmd` in the driver's MAIN shell context (effects persist for later `Run`s), wrapping the existing unexported `runMain`. Consumed by Task 6.

**Context:** B2a sets `PROJECT_ROOT` via `driver.Options.Env` at open. B2b must export MORE vars into the SAME driver AFTER open, before a block runs. The driver has an unexported `runMain(cmd, timeout)` (main-context, persists) but `internal/ui` can't call it. Export a thin wrapper.

- [ ] **Step 1: Write the failing test**

```go
func TestRunMain_ExportPersists(t *testing.T) {
	d, err := Open(Options{Shell: "bash"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	d.RunMain("export B2B_TEST=hello", 5*time.Second)
	res := d.Run("printf '%s' \"$B2B_TEST\"", 5*time.Second)
	if res.Output != "hello" {
		t.Fatalf("exported var not visible to later Run: output=%q", res.Output)
	}
}
```
(Match `Result`'s real output field name — read `driver.go` for `Result`; adjust `res.Output` if it differs.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/driver/ -run TestRunMain_ExportPersists`
Expected: FAIL — `d.RunMain` undefined.

- [ ] **Step 3: Implement** (in `internal/driver/driver.go`)

```go
// RunMain runs cmd in the driver's MAIN shell context (not the errexit subshell that
// Run uses), so side effects like `export` persist for subsequent Run calls. It is the
// exported counterpart to the internal runMain used at Open.
func (d *Driver) RunMain(cmd string, timeout time.Duration) {
	d.runMain(cmd, timeout)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/driver/ -run TestRunMain_ExportPersists`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/driver/driver.go internal/driver/driver_test.go
git commit -m "feat(driver): export RunMain for main-context env injection"
```

---

### Task 5: Thread front-matter `env` + project root into the model

**Files:**
- Modify: `internal/ui/model.go` (`loadPlaybookDocument` + new model fields)
- Modify: `internal/ui/main.go` (capture `env` + project root into the model on the `run --file` path)
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Produces: model fields `confirmEnv map[string]frontmatter.EnvValue`, `projectRoot string`, `gateSatisfied bool`; `loadPlaybookDocument` also returns the parsed `env`. Consumed by Tasks 6 + 7.

**Context (verbatim today):** `loadPlaybookDocument(content) (title, subtitle, body string)` parses the front matter but returns only title/subtitle/body — `fm.Env` is dropped. `loadPlaybookSource` calls it. In `main.go`'s `run --file` branch, `pendingProjectRoot` is consumed into `driver.Options.Env` then cleared — the model never keeps it.

- [ ] **Step 1: Write the failing test**

```go
func TestLoadPlaybookDocument_ReturnsEnv(t *testing.T) {
	doc := "---\nname: T\nenv:\n  FOO:\n    why: bar\n---\n# T\n\n```bash {id=fix}\ntrue\n```\n"
	_, _, _, env := loadPlaybookDocument(doc)
	if env == nil || env["FOO"].Why != "bar" {
		t.Fatalf("loadPlaybookDocument env = %v, want FOO.why=bar", env)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestLoadPlaybookDocument_ReturnsEnv`
Expected: FAIL — `loadPlaybookDocument` returns 3 values, not 4.

- [ ] **Step 3: Implement**

In `internal/ui/model.go`, change `loadPlaybookDocument` to also return the env, and add the model fields:
```go
func loadPlaybookDocument(content string) (title, subtitle, body string, env map[string]frontmatter.EnvValue) {
	fm, rest, ok := frontmatter.Parse(content)
	h1, stripped := playbookHeading(rest)
	body = stripped
	if ok {
		subtitle = fm.Description
		env = fm.Env
		if fm.Name != "" {
			title = fm.Name
			return title, subtitle, body, env
		}
	}
	title = h1
	return title, subtitle, body, env
}
```
Add to the `model` struct (near the other run-state fields):
```go
	// B2b pre-run variable confirmation
	confirmEnv    map[string]frontmatter.EnvValue // declared env (front matter); nil/empty → no gate
	projectRoot   string                          // heuristic root (the PROJECT_ROOT value)
	gateSatisfied bool                            // the gate ran (or wasn't needed) this session
	gate          *confirmGate                    // active gate state; nil when not confirming
```
Update `loadPlaybookSource` (and any other `loadPlaybookDocument` caller) to thread the new return value — `loadPlaybookSource` returns it up so `Main` can stash it. In `internal/ui/main.go`'s `run --file` branch, AFTER constructing the model and BEFORE clearing `pendingProjectRoot`, capture both:
```go
	mdl.confirmEnv = env          // from loadPlaybookSource's new return
	mdl.projectRoot = projectRoot // the same value injected into driver.Options.Env
```
(`projectRoot` is the local already computed from `pendingProjectRoot` at the driver-open site — capture it into the model rather than only into the env slice.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/main.go internal/ui/confirm_gate_test.go
git commit -m "feat(ui): thread front-matter env + project root into the model"
```

---

### Task 6: The reusable confirmation gate

**Files:**
- Modify: `internal/ui/confirm_gate.go` (the gate state machine + the export)
- Modify: `internal/ui/model.go` (handle the gate answer message in `Update`)
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Consumes: `groupSizes` (T1), `buildConfirmVars`/`confirmVar` (T2), `NewAsk(...affLabel,negLabel)` (T3), `Driver.RunMain` (T4), the model's `confirmEnv`/`projectRoot`/`gateSatisfied`/`gate` (T5), and the model ask machinery `m.ask`/`m.askMode`/`m.askCompletion`.
- Produces: `func (m model) beginGate(block Button) (model, tea.Cmd)` (invoked by Task 7 and, later, assisted-run); `gateAnswerMsg`; the `Update` arm advancing the gate.

**Context:** The model raises an overlay ask by setting `m.ask = input.NewAsk(...)`, `m.askMode = true`, and `m.askCompletion = func(value string, submitted bool) tea.Msg { ... }` (the viewer-initiated override the `r`/refine flow uses); `handleAskKey` routes keys to `m.ask` and, on done, invokes `m.askCompletion`. The overlay is single-shot, so the gate chains dialogs: each answer raises the next. The confirm field yields `"yes"`/`"no"`.

- [ ] **Step 1: Write the failing tests** (drive the gate through messages, no TTY)

```go
func gateModel(t *testing.T) model {
	m := model{
		confirmEnv:  map[string]frontmatter.EnvValue{"PROJECT_ROOT": {Why: "root"}, "FOO": {Why: "foo"}},
		projectRoot: "/proj",
	}
	return m
}

func TestGate_ConfirmRunsBlockOnce(t *testing.T) {
	m := gateModel(t)
	blk := Button{Kind: "run", BlockID: "fix", Payload: "echo hi"}
	m, _ = m.beginGate(blk)
	if !m.askMode || m.gate == nil {
		t.Fatal("beginGate should raise a dialog and set gate")
	}
	// Confirm the single group (2 vars ≤5 → 1 group): answer "yes".
	var cmd tea.Cmd
	m, cmd = m.advanceGate("yes", true)
	if m.gate != nil || !m.gateSatisfied {
		t.Fatalf("after confirm the gate should clear + be satisfied (gate=%v satisfied=%v)", m.gate, m.gateSatisfied)
	}
	if cmd == nil {
		t.Fatal("confirm should return a cmd (export + deferred block)")
	}
}

func TestGate_EscReturnsToReading(t *testing.T) {
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	m, _ = m.advanceGate("", false) // ESC
	if m.gate != nil || m.askMode || m.gateSatisfied {
		t.Fatalf("ESC must clear the gate, leave it unsatisfied, exit ask mode")
	}
}

func TestGate_CustomizeEditsValue(t *testing.T) {
	m := gateModel(t)
	m, _ = m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	m, _ = m.advanceGate("no", true) // Customize → first var line edit
	if !m.gate.customizing {
		t.Fatal("Customize should enter the per-var edit phase")
	}
	// edit FOO (first sorted var) then PROJECT_ROOT
	m, _ = m.advanceGate("/edited/foo", true)
	m, _ = m.advanceGate("/edited/root", true)
	if m.gate != nil || !m.gateSatisfied {
		t.Fatalf("after editing all vars the gate should finish")
	}
}

func TestGate_NoEnvRunsDirectly(t *testing.T) {
	m := model{} // empty confirmEnv
	m, cmd := m.beginGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if m.gate != nil || m.askMode {
		t.Fatal("no env → no gate")
	}
	if !m.gateSatisfied || cmd == nil {
		t.Fatal("no env → satisfied + run the block directly")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestGate_`
Expected: FAIL — `beginGate`/`advanceGate`/`confirmGate` undefined.

- [ ] **Step 3: Implement** (append to `confirm_gate.go`; add the `Update` arm to `model.go`)

In `confirm_gate.go`:
```go
import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// confirmGate holds the state of an in-progress pre-run variable confirmation.
type confirmGate struct {
	groups      [][]confirmVar    // balanced ≤5 groups
	values      map[string]string // name → final value (seeded from current, edited by Customize)
	order       []string          // flat var order (for customize indexing)
	gi          int               // current group index (confirm phase)
	ci          int               // current var index (customize phase, flat over the group)
	customizing bool              // editing the current group's vars
	block       Button            // the deferred block to run after the gate
}

type gateAnswerMsg struct {
	value     string
	submitted bool
}

// beginGate is the reusable entry point: if the playbook declares env vars and the gate
// is unsatisfied, it raises the first confirm dialog and defers the block; otherwise it
// marks satisfied and returns the block's run cmd directly. Callable from the first-block
// trigger (Task 7) and, later, the assisted-run start.
func (m model) beginGate(block Button) (model, tea.Cmd) {
	vars := buildConfirmVars(m.confirmEnv, m.projectRoot, os.Getenv)
	if len(vars) == 0 || m.gateSatisfied {
		m.gateSatisfied = true
		return m, m.emitAction(block)
	}
	g := &confirmGate{values: map[string]string{}, block: block}
	for _, v := range vars {
		g.values[v.Name] = v.Value
	}
	// partition vars into balanced groups
	i := 0
	for _, sz := range groupSizes(len(vars)) {
		g.groups = append(g.groups, vars[i:i+sz])
		i += sz
	}
	m.gate = g
	return m.raiseGroupConfirm()
}

// raiseGroupConfirm opens the confirm dialog for the current group.
func (m model) raiseGroupConfirm() (model, tea.Cmd) {
	g := m.gate
	var b strings.Builder
	b.WriteString("Confirm these variables for this run:\n\n")
	for _, v := range g.groups[g.gi] {
		fmt.Fprintf(&b, "  %s = %s\n", v.Name, g.values[v.Name])
	}
	m.ask = NewAsk("Variables", b.String(), "", "confirm", nil, "Confirm", "Customize")
	m.askMode = true
	m.askCompletion = func(value string, submitted bool) tea.Msg {
		return gateAnswerMsg{value: value, submitted: submitted}
	}
	return m, m.ask.Init()
}

// raiseVarEdit opens a prefilled line dialog for the current customize var.
func (m model) raiseVarEdit() (model, tea.Cmd) {
	g := m.gate
	v := g.groups[g.gi][g.ci]
	prompt := v.Name
	if v.Why != "" {
		prompt += " — " + v.Why
	}
	m.ask = NewAsk("Customize", prompt, g.values[v.Name], "line", nil, "", "")
	m.askMode = true
	m.askCompletion = func(value string, submitted bool) tea.Msg {
		return gateAnswerMsg{value: value, submitted: submitted}
	}
	return m, m.ask.Init()
}

// advanceGate consumes one dialog answer and drives the state machine.
func (m model) advanceGate(value string, submitted bool) (model, tea.Cmd) {
	g := m.gate
	if g == nil {
		return m, nil
	}
	m.askMode = false
	m.askCompletion = nil
	if !submitted { // ESC → cancel, return to reading, gate stays unsatisfied
		m.gate = nil
		return m, nil
	}
	if g.customizing {
		g.values[g.groups[g.gi][g.ci].Name] = value
		g.ci++
		if g.ci < len(g.groups[g.gi]) {
			return m.raiseVarEdit()
		}
		g.customizing = false
		g.ci = 0
		g.gi++
		return m.afterGroup()
	}
	// confirm phase
	if value == "no" { // Customize
		g.customizing = true
		g.ci = 0
		return m.raiseVarEdit()
	}
	g.gi++ // Confirm → next group
	return m.afterGroup()
}

// afterGroup advances to the next group's confirm, or finishes the gate.
func (m model) afterGroup() (model, tea.Cmd) {
	g := m.gate
	if g.gi < len(g.groups) {
		return m.raiseGroupConfirm()
	}
	// finished: export the final values, mark satisfied, run the deferred block.
	block := g.block
	exportCmd := buildExportCmd(g.values)
	m.gate = nil
	m.gateSatisfied = true
	drv := m.orchDriver()
	run := m.emitAction(block)
	return m, func() tea.Msg {
		if drv != nil && exportCmd != "" {
			drv.RunMain(exportCmd, 10*time.Second)
		}
		return nil
	}
	_ = run // see note below
}

// buildExportCmd shell-quotes the final values into a single export command.
func buildExportCmd(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export %s=%s; ", n, shellQuote(values[n]))
	}
	return b.String()
}

// shellQuote single-quotes s for POSIX shells ('→'\'').
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

NOTE on ordering in `afterGroup`: the export must complete BEFORE the block runs (the block reads the exported vars). `RunMain` is synchronous; sequence them in one cmd so the export precedes the run. Replace the tail of `afterGroup` with a single sequential cmd:
```go
	drv := m.orchDriver()
	m.gate = nil
	m.gateSatisfied = true
	return m, tea.Sequence(
		func() tea.Msg {
			if drv != nil && exportCmd != "" {
				drv.RunMain(exportCmd, 10*time.Second)
			}
			return nil
		},
		m.emitAction(block),
	)
```
(Use `tea.Sequence` so the export cmd runs to completion before the block action; remove the dead `run`/`_ = run` lines. If `emitAction` returns nil for a non-orchestrated model in tests, the test asserts a non-nil sequence cmd — `tea.Sequence` of the export alone is non-nil.)

Add `orchDriver` to `confirm_gate.go` (or `model.go`):
```go
// orchDriver returns the live run driver, or nil when none is attached (tests).
func (m model) orchDriver() *driver.Driver {
	if m.orch != nil {
		return m.orch.Drv
	}
	return nil
}
```
(Import `"github.com/Townk/ai-playbook/internal/driver"`. Confirm `orchestrator.Orchestrator.Drv` is exported; if it is unexported, add an accessor `func (o *Orchestrator) Driver() *driver.Driver` and use it.)

In `internal/ui/model.go` `Update`, add an arm handling the gate answer (place it with the other `case … Msg:` arms):
```go
	case gateAnswerMsg:
		return m.advanceGate(msg.value, msg.submitted)
```
Also ensure `handleAskKey` invokes `m.askCompletion` when set (it does for `r`/refine — verify the gate's `askCompletion` is honored the same way; if `handleAskKey` only calls `askReq.Respond`, route to `askCompletion` first when non-nil).

- [ ] **Step 4: Run the gate tests + the package**

Run: `go test ./internal/ui/ -run TestGate_`
Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/confirm_gate.go internal/ui/model.go internal/ui/confirm_gate_test.go
git commit -m "feat(ui): reusable pre-run variable confirmation gate"
```

---

### Task 7: First-block-run trigger

**Files:**
- Modify: `internal/ui/model.go` (the two `run`-button activation sites)
- Test: `internal/ui/confirm_gate_test.go`

**Interfaces:**
- Consumes: `beginGate` (T6), `gateSatisfied`/`confirmEnv` (T5).

**Context (verbatim today):** both the mouse-click path (~`model.go:756-760`) and the keyboard hint-mode path (~`model.go:910-914`) run a block with the same shape:
```go
if b.Kind == "run" {
	m = m.markRunning(b.BlockID)
	ac := m.emitAction(b)
	m.reflow()
	return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
}
```
B2b intercepts: if the gate is unsatisfied and the playbook declares env vars, run the gate (which defers the block) instead of `emitAction` now.

- [ ] **Step 1: Write the failing test**

```go
func TestTrigger_FirstRunGated(t *testing.T) {
	m := gateModel(t) // confirmEnv non-empty, gateSatisfied=false
	gated, _ := m.runOrGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if gated.gate == nil || !gated.askMode {
		t.Fatal("first run with env vars must open the gate, not run directly")
	}
}

func TestTrigger_SatisfiedRunsDirectly(t *testing.T) {
	m := gateModel(t)
	m.gateSatisfied = true
	direct, _ := m.runOrGate(Button{Kind: "run", BlockID: "fix", Payload: "x"})
	if direct.gate != nil {
		t.Fatal("once satisfied, runs must not re-open the gate")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestTrigger_`
Expected: FAIL — `runOrGate` undefined.

- [ ] **Step 3: Implement**

Add a shared helper to `confirm_gate.go` and call it from both run sites:
```go
// runOrGate runs block b directly, or — on the first run of an env-bearing playbook —
// opens the confirmation gate (which runs b after the user confirms). The caller marks
// the block running + reflows + batches the ticks as before; runOrGate returns the
// action cmd (direct run, or the gate's dialog/defer).
func (m model) runOrGate(b Button) (model, tea.Cmd) {
	if !m.gateSatisfied && len(m.confirmEnv) > 0 {
		return m.beginGate(b)
	}
	return m, m.emitAction(b)
}
```
Replace BOTH run-button sites' `ac := m.emitAction(b)` with the gated form, e.g.:
```go
if b.Kind == "run" {
	m = m.markRunning(b.BlockID)
	var ac tea.Cmd
	m, ac = m.runOrGate(b)
	m.reflow()
	return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
}
```
(`markRunning` still flips the block's spinner; if the gate is opened, the spinner shows behind the dialog and the real run starts on confirm — acceptable. If you prefer no premature spinner, move `markRunning` into the non-gated branch; keep it simple unless a test/review objects.)

- [ ] **Step 4: Run tests + the whole package (-race)**

Run: `go test ./internal/ui/ -run TestTrigger_`
Run: `go build ./... && go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/confirm_gate.go internal/ui/confirm_gate_test.go
git commit -m "feat(ui): gate the first block-run on variable confirmation"
```

---

## Self-Review

**Spec coverage (B2b):** grouping helper (T1) ✓; var-list builder (T2) ✓; Confirm/Customize labels (T3) ✓; driver export seam (T4) ✓; env+root threading (T5) ✓; reusable gate — confirm/customize/ESC/once-satisfied/export (T6) ✓; first-block-run trigger (T7) ✓. The gate is callable independently (`beginGate`) so assisted-run reuses it (T6 interface) ✓. Overlay-for-both-surfaces (Global Constraints + T6 uses `m.ask`/`askMode` directly) ✓.

**Deferred (NOT B2b):** the assisted-run feature itself (Phase-2 run modes — `beginGate` is built to be reused there); re-engagement → structured (B3); viewer-UX-polish; `file=`/diff.

**Type consistency:** `confirmVar{Name,Value,Why}` (T2) ↔ used in `confirmGate.groups` (T6); `groupSizes` (T1) ↔ partition in `beginGate` (T6); `NewAsk(...affLabel,negLabel)` (T3) ↔ both `raiseGroupConfirm`/`raiseVarEdit` (T6) and `openAsk` (T3 caller update); `Driver.RunMain` (T4) ↔ `orchDriver().RunMain` (T6); `confirmEnv`/`projectRoot`/`gateSatisfied`/`gate` (T5) ↔ `beginGate`/`runOrGate` (T6/T7); `gateAnswerMsg` (T6) ↔ `Update` arm + `askCompletion` (T6).

**Open items the implementer must confirm against real code (flagged, not placeheld):**
- T6: whether `orchestrator.Orchestrator.Drv` is exported (else add `Driver()` accessor); whether `handleAskKey` already routes to `m.askCompletion` (the `r`/refine path uses it — mirror it).
- T5: the exact `model` construction site in `main.go` to stash `confirmEnv`/`projectRoot`, and that `loadPlaybookSource` can carry the env up.
- T4: the real `Result` output field name in the driver test.
- T3: the full caller list for `input.NewAsk` (update each with `"", ""`).
