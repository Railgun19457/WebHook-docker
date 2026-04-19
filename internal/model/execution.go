package model

import "time"

type ExecutionStatus string

const (
	ExecutionStatusQueued  ExecutionStatus = "queued"
	ExecutionStatusRunning ExecutionStatus = "running"
	ExecutionStatusSuccess ExecutionStatus = "success"
	ExecutionStatusFailed  ExecutionStatus = "failed"
)

type StepResult struct {
	Command      string    `json:"command"`
	ExitCode     int       `json:"exit_code"`
	Stdout       string    `json:"stdout"`
	Stderr       string    `json:"stderr"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	DurationMS   int64     `json:"duration_ms"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type ExecutionRecord struct {
	RequestID    string          `json:"request_id"`
	HookID       string          `json:"hook_id"`
	Provider     string          `json:"provider"`
	EventType    string          `json:"event_type"`
	Status       ExecutionStatus `json:"status"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   time.Time       `json:"finished_at"`
	DurationMS   int64           `json:"duration_ms"`
	ExitCode     int             `json:"exit_code"`
	ErrorMessage string          `json:"error_message,omitempty"`
	SourceIP     string          `json:"source_ip"`
	Branch       string          `json:"branch,omitempty"`
	CommitSHA    string          `json:"commit_sha,omitempty"`
	StepResults  []StepResult    `json:"step_results,omitempty"`
}
