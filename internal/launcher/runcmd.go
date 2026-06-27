// runcmd.go — the `ai-playbook run` subcommand entrypoint, its argument
// resolution, and adapt-on-run (Task 9).
//
// `run` accepts a single playbook source, expressed one of three ways:
//
//   - run <slug>            a bare positional ⇒ implied --playbook <slug>
//   - run --playbook <slug> a saved playbook resolved through the store
//   - run --file <path>     a raw markdown file rendered as-is
//
// Exactly one source must be given; zero or more than one is an error.
//
// ADAPT-ON-RUN. A --playbook/slug source is store.Load'd, then ADAPTED to the
// target directory by ONE authoring-model call (paths/versions/project-specifics
// rewritten) before it renders. The adaptation is junk-guarded (ui.ValidatePlaybook):
// a result that is not a real playbook falls back to the original, no banner. The
// adapted body renders with an "adapted from <slug>" banner and a `d` original→adapted
// diff keybind (the --adapted-from / --orig-file flags). A --file source with FRONT
// MATTER adapts the same way; a raw --file with NO front matter renders as-is.
//
// Internal callers (serveCachedPlaybook, AnswerMain) do NOT go through RunMain:
// they reshape os.Args to `run --file <tmp>` and call ui.Main directly, so they
// bypass adapt-on-run (their temp files carry no front matter and render as-is).
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/store"
	"github.com/Townk/ai-playbook/internal/ui"
)

// storeLoadFn is the store.Load seam: resolves a slug to its Meta + body. Tests
// inject a fake so RunMain's adapt routing is exercised without a real store.
var storeLoadFn = store.Load

// adaptModelFn performs ONE authoring-model call for adapt-on-run: it takes the
// system + user prompt and returns the FULL collected output text (NOT streamed).
// Production wires liveAdapt (author.RunHarnessEvents collected to a buffer); tests
// inject a fake so RunMain's adapt routing runs without a live model.
var adaptModelFn = liveAdapt

// projectRootFn is the capture.ProjectRoot seam (the off-mux target-dir fallback).
var projectRootFn = capture.ProjectRoot

// RunMain is the `ai-playbook run` subcommand: it owns config loading + the
// configured-shell hand-off (ui stays config-agnostic), resolves the run
// argument, ADAPTS a store/front-matter playbook to its target dir, and renders
// the result through ui.Main (via uiMainFn).
func RunMain() int {
	// cfg is always non-nil (config.Load returns Default on error). The `run`
	// subcommand opens its own driver, so honor the configured shell — ui stays
	// config-agnostic and receives the selector as DATA via SetShell.
	cfg, _ := config.Load()
	ui.SetShell(cfg.Driver.Shell)

	kind, value, err := resolveRunArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 2
	}

	switch kind {
	case "file":
		return runFile(value)
	case "playbook":
		return runPlaybook(value)
	}
	return 0
}

// runPlaybook resolves a slug through the store, adapts it to its target dir, and
// renders the adapted body.
func runPlaybook(slug string) int {
	meta, body, lerr := storeLoadFn(slug)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", lerr)
		return 1
	}
	target := resolveTargetDir(meta)
	renderFile, origFile, bannerSlug, aerr := adaptOnRun(meta, body, target, adaptModelFn)
	if aerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", aerr)
		return 1
	}
	return renderAdapted(renderFile, target, bannerSlug, origFile)
}

// runFile renders a raw markdown file. A file WITH front matter is treated as a
// playbook and adapted to its (front-matter) workdir; a raw file WITHOUT front
// matter renders as-is (the Task 8 behavior; this is also the internal-caller
// temp-file shape, though those callers bypass RunMain).
func runFile(file string) int {
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", rerr)
		return 1
	}
	fm, fmBody, ok := frontmatter.Parse(string(data))
	if !ok {
		// No front matter → render as-is via the `run --file` path (no adapt).
		saved := os.Args
		os.Args = []string{os.Args[0], "run", "--file", file}
		code := uiMainFn()
		os.Args = saved
		return code
	}
	// Has front matter → adapt to its workdir, like a store playbook.
	stem := strings.TrimSuffix(filepath.Base(file), ".md")
	meta := store.Meta{
		Slug:        stem,
		Name:        fm.Name,
		Description: fm.Description,
		Workdir:     fm.Workdir,
		Path:        file,
	}
	target := resolveTargetDir(meta)
	renderFile, origFile, bannerSlug, aerr := adaptOnRun(meta, fmBody, target, adaptModelFn)
	if aerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", aerr)
		return 1
	}
	return renderAdapted(renderFile, target, bannerSlug, origFile)
}

// renderAdapted reshapes os.Args to the adapt-on-run `run --file` form ui.Main
// parses and renders. bannerSlug is "" when the adaptation was junk-guarded back to
// the original — then --adapted-from / --orig-file are omitted so no banner/diff
// shows.
func renderAdapted(renderFile, target, bannerSlug, origFile string) int {
	saved := os.Args
	args := []string{os.Args[0], "run", "--file", renderFile, "--cwd", target}
	if bannerSlug != "" {
		args = append(args, "--adapted-from", bannerSlug, "--orig-file", origFile)
	}
	os.Args = args
	code := uiMainFn()
	os.Args = saved
	return code
}

// resolveTargetDir resolves the directory the playbook should be adapted FOR:
//
//   - default = the playbook's front-matter workdir (tilde-expanded);
//   - if that is empty OR the directory no longer exists → ASK the user via the
//     input float when a real mux is present;
//   - OFF-MUX edge: there is no float to spawn (tests / CI / a bare shell with no
//     zellij/tmux), so fall back to capture.ProjectRoot() rather than block.
func resolveTargetDir(meta store.Meta) string {
	target := expandTilde(meta.Workdir)
	if target != "" && dirExists(target) {
		return target
	}
	m := mux.Load()
	if mux.IsNull(m) {
		// No-float edge: nothing to ask with — use the current project root.
		return projectRootFn()
	}
	selfExe, _ := os.Executable()
	asker := floatinput.Asker{SelfExe: selfExe, Mux: m}
	res, err := asker.Ask(floatinput.Request{
		Type:   "text",
		Title:  "Target directory",
		Prompt: "Which directory should this playbook be adapted for?",
		Value:  target,
	})
	if err != nil || !res.Submitted || strings.TrimSpace(res.Value) == "" {
		return projectRootFn()
	}
	return expandTilde(strings.TrimSpace(res.Value))
}

// adaptOnRun performs the adapt-on-run authoring pass for a resolved playbook:
//
//	b. ONE authoring-model call (adaptFn), collected to a FULL buffer (not streamed);
//	c. junk-guard: if the result is not a valid playbook (ui.ValidatePlaybook),
//	   fall back to the original body and clear the banner slug;
//	d. write the body to render (renderFile) and the original (origFile, for the
//	   `d` diff) to temp files.
//
// It returns the render-file path, the original-file path, the banner slug (the
// playbook's slug, or "" when junk-guarded back to the original), and any error.
// adaptFn is injected so the whole pass is unit-testable without a live model.
func adaptOnRun(meta store.Meta, body, targetDir string, adaptFn func(sys, user string) (string, error)) (renderFile, origFile, bannerSlug string, err error) {
	// b. one authoring-model call, collected to a FULL buffer.
	adapted, aerr := adaptFn(author.AdaptPrompt(meta, targetDir), body)
	if aerr != nil {
		return "", "", "", aerr
	}

	// c. junk-guard — REUSE ui.ValidatePlaybook (isValidPlaybook + Render's block
	// count). A narration / non-playbook result falls back to the original, no banner.
	bannerSlug = meta.Slug
	if !ui.ValidatePlaybook(adapted) {
		adapted = body
		bannerSlug = ""
	}

	// d. write the render body + the original (for the diff) to temp files.
	renderFile, err = writeTempMarkdown("adapted", adapted)
	if err != nil {
		return "", "", "", err
	}
	origFile, err = writeTempMarkdown("orig", body)
	if err != nil {
		return "", "", "", err
	}
	return renderFile, origFile, bannerSlug, nil
}

// liveAdapt is the production adaptModelFn: ONE owned-harness invocation
// (author.RunHarnessEvents) with DEFAULT thinking (the authoring opts), collected
// to a full buffer — the authoritative Final text, else the accumulated TextDelta
// (mirroring author.runMetadataOnce's body fallback). No streaming: the adapted
// document is validated BEFORE it is displayed.
func liveAdapt(sys, user string) (string, error) {
	cfg, _ := config.Load()
	events, wait, err := author.RunHarnessEvents(sys, user, author.AuthorOptions{Cfg: cfg})
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

// writeTempMarkdown writes content to a temp *.md file (the tag distinguishes the
// adapted-render vs original-diff files) and returns its path. The files are left
// for the OS /tmp reap — ui.Main reads them after this returns.
func writeTempMarkdown(tag, content string) (string, error) {
	f, err := os.CreateTemp("", "ai-playbook-"+tag+"-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, werr := f.WriteString(content); werr != nil {
		f.Close()
		os.Remove(name)
		return "", werr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(name)
		return "", cerr
	}
	return name, nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// expandTilde expands a leading "~" / "~/" in p to the user's home dir (config's
// own expandTilde is unexported; this mirrors it for the launcher's workdir
// resolution). A "~" alone or a "~/..." prefix expands; "~user" is left verbatim.
func expandTilde(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// resolveRunArgs resolves the single playbook source from the `run` arguments.
// Exactly one of {bare positional, --playbook, --file} must be present:
//
//   - --file <path>      → ("file", path)
//   - --playbook <slug>  → ("playbook", slug)
//   - a bare positional  → ("playbook", slug)  (implied --playbook)
//
// Zero sources or more than one is an error. When a slug is supplied both as a
// positional and via --playbook (or --file) it counts as two sources → an error,
// so the caller's intent is never ambiguous.
func resolveRunArgs(args []string) (kind, value string, err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var playbook, file string
	fs.StringVar(&playbook, "playbook", "", "slug of a saved playbook to run")
	fs.StringVar(&file, "file", "", "path to a markdown file to run")
	if perr := fs.Parse(args); perr != nil {
		return "", "", perr
	}
	// The stdlib flag package stops at the FIRST non-flag token, so anything after a
	// bare positional (e.g. `run build --file x`) lands here unparsed. Treat any
	// leftover beyond the single positional as a conflict — the source must be
	// unambiguous.
	rest := fs.Args()
	if len(rest) > 1 {
		return "", "", fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
	}
	positional := ""
	if len(rest) == 1 {
		positional = rest[0]
	}

	count := 0
	for _, s := range []string{playbook, file, positional} {
		if s != "" {
			count++
		}
	}
	switch {
	case count == 0:
		return "", "", fmt.Errorf("specify a playbook: run <slug> | --playbook <slug> | --file <path>")
	case count > 1:
		return "", "", fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
	case file != "":
		return "file", file, nil
	case playbook != "":
		return "playbook", playbook, nil
	default:
		return "playbook", positional, nil
	}
}
