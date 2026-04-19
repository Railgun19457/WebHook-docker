package executor

import (
	"context"

	"webhook-docker/internal/config"
	"webhook-docker/internal/model"
)

type Request struct {
	HookID     string
	Commands   []string
	SSHProfile *config.SSHProfile
}

type Result struct {
	ExitCode int
	Steps    []model.StepResult
}

type Executor interface {
	Execute(ctx context.Context, req Request) (Result, error)
}
