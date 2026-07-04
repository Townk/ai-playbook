package askcli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/input"
)

// capture redirects os.Stdout/os.Stderr around fn and returns its exit code plus
// the captured streams.
func capture(fn func() int) (code int, out, errOut string) {
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	outC := make(chan string)
	errC := make(chan string)
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, rOut); outC <- b.String() }()
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, rErr); errC <- b.String() }()
	code = fn()
	_ = wOut.Close()
	_ = wErr.Close()
	out, errOut = <-outC, <-errC
	os.Stdout, os.Stderr = origOut, origErr
	return
}

// stubWidget installs a runWidget seam that records the invocation and returns
// the given outcome, plus a TTY-present stub. It restores both on cleanup and
// returns a pointer to the captured invocation.
func stubWidget(t *testing.T, out widgetOutcome, err error) *widgetInvocation {
	t.Helper()
	origRun, origTTY := runWidget, hasTTY
	var got widgetInvocation
	runWidget = func(inv widgetInvocation) (widgetOutcome, error) {
		got = inv
		return out, err
	}
	hasTTY = func() bool { return true }
	t.Cleanup(func() { runWidget, hasTTY = origRun, origTTY })
	return &got
}

// runAsk runs Run with the given ask args (program name prepended) under capture.
func runAsk(args ...string) (int, string, string) {
	return capture(func() int { return Run(append([]string{"ask"}, args...)) })
}

// --- confirm: args → options mapping ----------------------------------------

func TestConfirmOptionsMapping(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want input.ConfirmOptions
	}{
		{
			name: "defaults",
			args: []string{"confirm", "Proceed?"},
			want: input.ConfirmOptions{Variant: "default", Prompt: "Proceed?", Affirmative: "Yes", Negative: "No", DefaultNegative: false, Padding: 1, Inset: 1, Width: 50},
		},
		{
			name: "danger forces default negative",
			args: []string{"confirm", "Delete?", "--danger"},
			want: input.ConfirmOptions{Variant: "danger", Prompt: "Delete?", Affirmative: "Yes", Negative: "No", DefaultNegative: true, Padding: 1, Inset: 1, Width: 50},
		},
		{
			name: "warning variant",
			args: []string{"confirm", "Careful?", "--warning"},
			want: input.ConfirmOptions{Variant: "warning", Prompt: "Careful?", Affirmative: "Yes", Negative: "No", Padding: 1, Inset: 1, Width: 50},
		},
		{
			name: "labels, default side, title, dims",
			args: []string{"confirm", "Q", "--affirmative", "Sure", "--negative", "Nope", "--default", "negative", "--title", "T", "--width", "42", "--padding", "2", "--inset", "3"},
			want: input.ConfirmOptions{Variant: "default", Title: "T", Prompt: "Q", Affirmative: "Sure", Negative: "Nope", DefaultNegative: true, Padding: 2, Inset: 3, Width: 42},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
			code, _, _ := runAsk(tc.args...)
			if code != exitOK {
				t.Fatalf("exit = %d, want 0", code)
			}
			tc.want.Theme = input.DefaultTheme()
			if got.confirm != tc.want {
				t.Errorf("options =\n %+v\nwant\n %+v", got.confirm, tc.want)
			}
		})
	}
}

// --- confirm: exit codes + --print ------------------------------------------

func TestConfirmExitAndPrint(t *testing.T) {
	tests := []struct {
		name     string
		outcome  widgetOutcome
		print    bool
		wantCode int
		wantOut  string
	}{
		{"yes silent", widgetOutcome{confirm: "yes"}, false, 0, ""},
		{"no silent", widgetOutcome{confirm: "no"}, false, 1, ""},
		{"cancel", widgetOutcome{cancelled: true}, false, 130, ""},
		{"yes print", widgetOutcome{confirm: "yes"}, true, 0, "yes\n"},
		{"no print", widgetOutcome{confirm: "no"}, true, 1, "no\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubWidget(t, tc.outcome, nil)
			args := []string{"confirm", "Q?"}
			if tc.print {
				args = append(args, "--print")
			}
			code, out, _ := runAsk(args...)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}
		})
	}
}

// --- line / text: value on stdout, cancel 130 -------------------------------

func TestLineTextOutput(t *testing.T) {
	t.Run("line submit", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{value: "hello"}, nil)
		code, out, _ := runAsk("line", "Name?", "--value", "init", "--placeholder", "ph", "--icon", ">")
		if code != 0 || out != "hello\n" {
			t.Fatalf("code=%d out=%q", code, out)
		}
		want := input.LineOptions{Theme: input.DefaultTheme(), Variant: "default", Prompt: "Name?", Value: "init", Placeholder: "ph", Icon: ">", Padding: 1, Inset: 1, Width: 50}
		if got.line != want {
			t.Errorf("options=\n%+v\nwant\n%+v", got.line, want)
		}
	})
	t.Run("line cancel", func(t *testing.T) {
		stubWidget(t, widgetOutcome{cancelled: true}, nil)
		code, out, _ := runAsk("line", "Name?")
		if code != 130 || out != "" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
	t.Run("text submit maps height", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{value: "body"}, nil)
		code, out, _ := runAsk("text", "Notes", "--height", "7")
		if code != 0 || out != "body\n" {
			t.Fatalf("code=%d out=%q", code, out)
		}
		if got.text.Height != 7 || got.text.Prompt != "Notes" {
			t.Errorf("text options = %+v", got.text)
		}
	})
}

// --- choose: single, multi one-per-line, options mapping --------------------

func TestChoose(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{value: "b"}, nil)
		code, out, _ := runAsk("choose", "Pick", "a", "b", "c")
		if code != 0 || out != "b\n" {
			t.Fatalf("code=%d out=%q", code, out)
		}
		if got.choose.Prompt != "Pick" || strings.Join(got.choose.Options, ",") != "a,b,c" {
			t.Errorf("choose options = %+v", got.choose)
		}
	})
	t.Run("multi one per line", func(t *testing.T) {
		stubWidget(t, widgetOutcome{value: "a\nc"}, nil)
		code, out, _ := runAsk("choose", "Pick", "a", "b", "c", "--multi")
		if code != 0 || out != "a\nc\n" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
	t.Run("other flag", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{value: "x"}, nil)
		runAsk("choose", "Pick", "a", "--other", "Custom")
		if got.choose.Other != "Custom" {
			t.Errorf("other = %q, want Custom", got.choose.Other)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		stubWidget(t, widgetOutcome{cancelled: true}, nil)
		code, _, _ := runAsk("choose", "Pick", "a")
		if code != 130 {
			t.Fatalf("code=%d", code)
		}
	})
}

// --- `--` terminator: dashed positionals survive verbatim -------------------

func TestDashDashTerminator(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantP     string
		wantItems []string
		wantMulti bool
	}{
		{
			name:      "two dashed items",
			args:      []string{"choose", "Pick", "--", "-foo", "--bar"},
			wantP:     "Pick",
			wantItems: []string{"-foo", "--bar"},
		},
		{
			name:      "three dashed items",
			args:      []string{"choose", "Pick", "--", "-a", "-b", "-c"},
			wantP:     "Pick",
			wantItems: []string{"-a", "-b", "-c"},
		},
		{
			name:      "normal then dashed",
			args:      []string{"choose", "Pick", "--", "normal", "-dashed"},
			wantP:     "Pick",
			wantItems: []string{"normal", "-dashed"},
		},
		{
			name:      "flag after prompt plus dashed tail",
			args:      []string{"choose", "Pick", "--multi", "--", "-x", "--y"},
			wantP:     "Pick",
			wantItems: []string{"-x", "--y"},
			wantMulti: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stubWidget(t, widgetOutcome{value: "x"}, nil)
			code, _, errOut := runAsk(tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, errOut)
			}
			if got.choose.Prompt != tc.wantP {
				t.Errorf("prompt = %q, want %q", got.choose.Prompt, tc.wantP)
			}
			if strings.Join(got.choose.Options, "\x00") != strings.Join(tc.wantItems, "\x00") {
				t.Errorf("items = %q, want %q", got.choose.Options, tc.wantItems)
			}
			if got.choose.Multi != tc.wantMulti {
				t.Errorf("multi = %v, want %v", got.choose.Multi, tc.wantMulti)
			}
		})
	}
}

// TestDashedFlagValue asserts a dashed string reaches a flag via the =-form
// (`--negative=--weird`), and that a bare `--negative` left dangling by the
// `--` split is a usage error rather than silently capturing `--` as its value.
func TestDashedFlagValue(t *testing.T) {
	t.Run("equals form carries a dashed value", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
		code, _, _ := runAsk("confirm", "Q", "--negative=--weird")
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if got.confirm.Negative != "--weird" {
			t.Errorf("negative = %q, want --weird", got.confirm.Negative)
		}
	})
	t.Run("dangling flag before -- is a usage error", func(t *testing.T) {
		stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
		code, _, errOut := runAsk("confirm", "Q", "--negative", "--", "--oops")
		if code != exitUsage {
			t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
		}
		if !strings.Contains(errOut, "flag needs an argument") {
			t.Errorf("stderr = %q", errOut)
		}
	})
}

// --- form: JSON round-trip, key=value shell-quoted, --json, errors ----------

func TestFormSpecParsing(t *testing.T) {
	spec := `[
	  {"type":"line","key":"name","prompt":"Name"},
	  {"type":"choose","key":"color","prompt":"Color","options":["red","blue"],"multi":true},
	  {"type":"confirm","key":"ok","prompt":"OK?","default":"negative"}
	]`
	fields, err := parseJSONFormSpec([]byte(spec))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("got %d fields", len(fields))
	}
	if fields[0].Type != "line" || fields[0].Key != "name" || fields[0].Prompt != "Name" {
		t.Errorf("field0 = %+v", fields[0])
	}
	if fields[1].Type != "choose" || !fields[1].Multi || strings.Join(fields[1].Options, ",") != "red,blue" {
		t.Errorf("field1 = %+v", fields[1])
	}
	if fields[2].Type != "confirm" || !fields[2].DefaultNegative {
		t.Errorf("field2 = %+v", fields[2])
	}
}

func TestFormMalformedSpec(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"broken json", `[{"type":"line",`},
		{"missing key", `[{"type":"line","prompt":"x"}]`},
		{"missing type", `[{"key":"k"}]`},
		{"unknown field key", `[{"type":"line","key":"k","promt":"typo"}]`},
		{"trailing garbage", `[{"type":"line","key":"k"}] garbage`},
		{"trailing second value", `[{"type":"line","key":"k"}][]`},
		{"bogus default", `[{"type":"confirm","key":"k","default":"bogus"}]`},
		{"bad type", `[{"type":"slider","key":"k"}]`},
		{"empty array", `[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubWidget(t, widgetOutcome{}, nil)
			code, _, errOut := capture(func() int {
				return runFormWithStdin(tc.spec)
			})
			if code != exitUsage {
				t.Errorf("exit = %d, want 2", code)
			}
			if n := strings.Count(strings.TrimRight(errOut, "\n"), "\n"); n != 0 {
				t.Errorf("stderr not one line: %q", errOut)
			}
			if !strings.HasPrefix(errOut, "ask form:") {
				t.Errorf("stderr = %q, want ask form: prefix", errOut)
			}
		})
	}
}

// runFormWithStdin feeds spec on stdin and runs `ask form`.
func runFormWithStdin(spec string) int {
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() { _, _ = io.WriteString(w, spec); _ = w.Close() }()
	defer func() { os.Stdin = origStdin }()
	return Run([]string{"ask", "form"})
}

func TestFormOutput(t *testing.T) {
	spec := `[{"type":"line","key":"name","prompt":"Name"},{"type":"line","key":"msg","prompt":"Msg"}]`
	pairs := []input.FormPair{{Key: "name", Value: "ab"}, {Key: "msg", Value: "it's ok"}}

	t.Run("key=value shell-quoted", func(t *testing.T) {
		stubWidget(t, widgetOutcome{formPairs: pairs}, nil)
		code, out, _ := capture(func() int { return runFormSpecFlag(t, spec) })
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		want := "name='ab'\nmsg='it'\\''s ok'\n"
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("--json object", func(t *testing.T) {
		stubWidget(t, widgetOutcome{formPairs: pairs}, nil)
		code, out, _ := capture(func() int { return runFormSpecFlagJSON(t, spec) })
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
			t.Fatalf("output not JSON: %v (%q)", err, out)
		}
		if got["name"] != "ab" || got["msg"] != "it's ok" {
			t.Errorf("json = %v", got)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		stubWidget(t, widgetOutcome{cancelled: true}, nil)
		code, _, _ := capture(func() int { return runFormSpecFlag(t, spec) })
		if code != 130 {
			t.Fatalf("code=%d", code)
		}
	})
}

// runFormSpecFlag writes spec to a temp file and runs `ask form --spec <file>`.
func runFormSpecFlag(t *testing.T, spec string) int {
	t.Helper()
	f := t.TempDir() + "/spec.json"
	if err := os.WriteFile(f, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	return Run([]string{"ask", "form", "--spec", f})
}

func runFormSpecFlagJSON(t *testing.T, spec string) int {
	t.Helper()
	f := t.TempDir() + "/spec.json"
	if err := os.WriteFile(f, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	return Run([]string{"ask", "form", "--spec", f, "--json"})
}

// --- env fallback precedence (flag > ASK_* env > default) -------------------

func TestThemeEnvPrecedence(t *testing.T) {
	t.Run("default when no env no flag", func(t *testing.T) {
		got := stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
		runAsk("confirm", "Q")
		if got.confirm.Theme.Accent != input.DefaultTheme().Accent {
			t.Errorf("accent = %q, want default", got.confirm.Theme.Accent)
		}
	})
	t.Run("env wins over default", func(t *testing.T) {
		t.Setenv("ASK_THEME_ACCENT", "#111111")
		got := stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
		runAsk("confirm", "Q")
		if got.confirm.Theme.Accent != "#111111" {
			t.Errorf("accent = %q, want env #111111", got.confirm.Theme.Accent)
		}
	})
	t.Run("flag wins over env", func(t *testing.T) {
		t.Setenv("ASK_THEME_ACCENT", "#111111")
		got := stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
		runAsk("confirm", "Q", "--theme-accent", "#222222")
		if got.confirm.Theme.Accent != "#222222" {
			t.Errorf("accent = %q, want flag #222222", got.confirm.Theme.Accent)
		}
	})
}

// --- usage / version / unknown ----------------------------------------------

func TestUnknownSubcommand(t *testing.T) {
	code, _, errOut := runAsk("frobnicate")
	if code != exitUsage {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown subcommand") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestUnknownFlag(t *testing.T) {
	stubWidget(t, widgetOutcome{confirm: "yes"}, nil)
	code, _, errOut := runAsk("confirm", "Q", "--nope")
	if code != exitUsage {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "Usage:") {
		t.Errorf("stderr missing usage: %q", errOut)
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runAsk("--help")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	for _, sub := range []string{"confirm", "line", "text", "choose", "form"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help missing %q", sub)
		}
	}
}

func TestSubcommandHelp(t *testing.T) {
	code, out, _ := runAsk("confirm", "-h")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "--danger") || !strings.Contains(out, "--measure") {
		t.Errorf("confirm help = %q", out)
	}
}

func TestVersion(t *testing.T) {
	origVer := Version
	Version = "v9.9.9"
	defer func() { Version = origVer }()
	for _, flag := range []string{"--version", "-v"} {
		code, out, _ := runAsk(flag)
		if code != 0 || strings.TrimSpace(out) != "v9.9.9" {
			t.Errorf("%s: code=%d out=%q", flag, code, out)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		ldflag, buildVer string
		buildOK          bool
		want             string
	}{
		{"v1.2.3", "v0.0.0", true, "v1.2.3"},
		{"dev", "v0.6.1", true, "v0.6.1"},
		{"dev", "(devel)", true, "dev"},
		{"dev", "", false, "dev"},
	}
	for _, tc := range tests {
		if got := resolveVersion(tc.ldflag, tc.buildVer, tc.buildOK); got != tc.want {
			t.Errorf("resolveVersion(%q,%q,%v) = %q, want %q", tc.ldflag, tc.buildVer, tc.buildOK, got, tc.want)
		}
	}
}

// --- no-TTY -----------------------------------------------------------------

func TestNoTTY(t *testing.T) {
	origRun, origTTY := runWidget, hasTTY
	hasTTY = func() bool { return false }
	ran := false
	runWidget = func(widgetInvocation) (widgetOutcome, error) { ran = true; return widgetOutcome{}, nil }
	t.Cleanup(func() { runWidget, hasTTY = origRun, origTTY })

	code, _, errOut := runAsk("confirm", "Q")
	if code != exitUsage {
		t.Errorf("exit = %d, want 2", code)
	}
	if ran {
		t.Error("widget ran despite no TTY")
	}
	if !strings.Contains(errOut, "terminal is required") {
		t.Errorf("stderr = %q", errOut)
	}
}

// --- shell quoting ----------------------------------------------------------

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"":         "''",
		"plain":    "'plain'",
		"a b":      "'a b'",
		"it's":     `'it'\''s'`,
		"a\nb":     "'a\nb'",
		"$(rm -r)": "'$(rm -r)'",
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
