package autorun

import (
	"fmt"
	"io"
)

// Step is a unit handed to a StepRunner.
type Step struct {
	ID, Command string
	Lang        string // fence language; a script block's runner assembles its interpreter invocation from this
	Kind        StepKind
}

// StepRunner executes one step and returns its exit + captured output path +
// whether the step was aborted by an interrupt signal (as opposed to merely
// exiting non-zero on its own).
type StepRunner interface {
	RunStep(s Step) (exit int, outputPath string, cancelled bool)
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
	wasCancelled := false

	for {
		b, ok := NextRunnable(cfg.Blocks, status)
		if !ok {
			break
		}

		fmt.Fprintf(cfg.Out, "[%s] %s\n", b.ID, b.Command)
		exit, out, cancelled := r.RunStep(Step{ID: b.ID, Command: b.Command, Lang: b.Lang, Kind: b.Kind})
		fmt.Fprintln(cfg.Out)

		st := statusFor(exit)
		if cancelled {
			st = StatusCancelled
		}
		results = append(results, StepResult{
			ID:         b.ID,
			Command:    b.Command,
			Exit:       exit,
			Status:     st,
			OutputPath: out,
		})

		if cancelled {
			status[b.ID] = StatusFailed
			failedExit = exit
			wasCancelled = true
			break
		}

		if exit == 0 {
			status[b.ID] = StatusOK
		} else {
			status[b.ID] = StatusFailed
			failedExit = exit
			break
		}
	}

	if failedExit != 0 && cfg.AutoRollback && !wasCancelled {
		pairs := RollbackPairs(cfg.Blocks, status)
		for _, pair := range pairs {
			origin, target := pair[0], pair[1]
			command := commandFor(cfg.Blocks, target)
			fmt.Fprintf(cfg.Out, "[%s] %s\n", target, command)
			exit, out, _ := r.RunStep(Step{ID: target, Command: command, Lang: langFor(cfg.Blocks, target), Kind: KindRun})
			fmt.Fprintln(cfg.Out)

			status[origin] = StatusRolledBack
			for i := range results {
				if results[i].ID == origin {
					results[i].Status = StatusRolledBack
					break
				}
			}
			results = append(results, StepResult{
				ID:         target,
				Command:    command,
				Exit:       exit,
				Status:     statusFor(exit),
				OutputPath: out,
			})
		}
	}

	if cfg.LogDir != "" {
		_, _ = WriteRunLog(cfg.LogDir, cfg.Stamp, cfg.Slug, results)
	}
	fmt.Fprintln(cfg.Out)
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

// langFor looks up a block's fence language by id, so a rollback target runs
// through the same canonical payload assembly (interpreter for a script block)
// as a forward step. Empty when the id is unknown or the block has no language.
func langFor(blocks []Block, id string) string {
	for _, b := range blocks {
		if b.ID == id {
			return b.Lang
		}
	}
	return ""
}
