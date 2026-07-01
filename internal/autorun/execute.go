package autorun

import (
	"fmt"
	"io"
)

// Step is a unit handed to a StepRunner.
type Step struct {
	ID, Command string
	Kind        StepKind
}

// StepRunner executes one step and returns its exit + captured output path.
// Task 4 supplies the real orchestrator-backed impl; tests supply a fake.
type StepRunner interface {
	RunStep(s Step) (exit int, outputPath string)
}

// Config drives Execute. Out receives the streamed per-step headers + final summary.
type Config struct {
	Blocks       []Block
	AutoRollback bool
	Out          io.Writer
	LogDir       string // cache.DefaultRoot(); "" skips the log file
	Stamp        string // timestamp for the log filename
	Slug         string
}

// Execute runs the forward loop over an injected runner and returns the process exit
// code: 0 iff every step ran ok; else the failed step's exit code (min 1).
func Execute(cfg Config, r StepRunner) int {
	status := map[string]string{}
	var results []StepResult
	failedExit := 0

	for {
		b, ok := NextRunnable(cfg.Blocks, status)
		if !ok {
			break
		}

		fmt.Fprintf(cfg.Out, "[%s] %s\n", b.ID, b.Command)
		exit, out := r.RunStep(Step{ID: b.ID, Command: b.Command, Kind: b.Kind})

		results = append(results, StepResult{
			ID:         b.ID,
			Command:    b.Command,
			Exit:       exit,
			Status:     statusFor(exit),
			OutputPath: out,
		})

		if exit == 0 {
			status[b.ID] = StatusOK
		} else {
			status[b.ID] = StatusFailed
			failedExit = exit
			break
		}
	}

	if failedExit != 0 && cfg.AutoRollback {
		pairs := RollbackPairs(cfg.Blocks, status)
		for _, pair := range pairs {
			origin, target := pair[0], pair[1]
			command := commandFor(cfg.Blocks, target)
			exit, out := r.RunStep(Step{ID: target, Command: command, Kind: KindRun})
			status[origin] = StatusRolledBack
			results = append(results, StepResult{
				ID:         target,
				Command:    command,
				Exit:       exit,
				Status:     StatusRolledBack,
				OutputPath: out,
			})
		}
	}

	if cfg.LogDir != "" {
		_, _ = WriteRunLog(cfg.LogDir, cfg.Stamp, cfg.Slug, results)
	}
	fmt.Fprint(cfg.Out, Summarize(results))

	if failedExit == 0 {
		return 0
	}
	if failedExit < 1 {
		return 1
	}
	return failedExit
}

// statusFor maps a step's exit code to its StepResult status.
func statusFor(exit int) string {
	if exit == 0 {
		return StatusOK
	}
	return StatusFailed
}

// commandFor looks up a block's command by id.
func commandFor(blocks []Block, id string) string {
	for _, b := range blocks {
		if b.ID == id {
			return b.Command
		}
	}
	return ""
}
