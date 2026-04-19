package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"webhook-docker/internal/model"
)

type LocalExecutor struct {
	logger *slog.Logger
}

func NewLocalExecutor(logger *slog.Logger) *LocalExecutor {
	return &LocalExecutor{logger: logger}
}

func (e *LocalExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	var result Result

	for _, commandLine := range req.Commands {
		step, err := e.runCommand(ctx, commandLine)
		result.Steps = append(result.Steps, step)
		if err != nil {
			result.ExitCode = step.ExitCode
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, fmt.Errorf("local command failed: %w", err)
		}
	}

	result.ExitCode = 0
	return result, nil
}

func (e *LocalExecutor) runCommand(ctx context.Context, commandLine string) (model.StepResult, error) {
	startedAt := time.Now()
	name, args := shellCommand(commandLine)
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	finishedAt := time.Now()

	step := model.StepResult{
		Command:    commandLine,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		Stdout:     strings.TrimSpace(stdout.String()),
		Stderr:     strings.TrimSpace(stderr.String()),
	}

	if err != nil {
		step.ErrorMessage = err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			step.ExitCode = 124
			return step, context.DeadlineExceeded
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			step.ExitCode = exitErr.ExitCode()
			return step, err
		}
		step.ExitCode = 1
		return step, err
	}

	step.ExitCode = 0
	if e.logger != nil {
		e.logger.Debug("local command executed", "command", commandLine, "duration_ms", step.DurationMS)
	}
	return step, nil
}

func shellCommand(commandLine string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", commandLine}
	}
	return "/bin/sh", []string{"-lc", commandLine}
}
