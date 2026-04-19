package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"webhook-docker/internal/config"
	"webhook-docker/internal/executor"
	"webhook-docker/internal/model"
	"webhook-docker/internal/security"
	"webhook-docker/internal/store"
)

const maxPayloadBytes = 2 << 20

type Service struct {
	logger               *slog.Logger
	cfg                  *config.Config
	hooksByID            map[string]config.HookConfig
	hooksByPath          map[string]config.HookConfig
	semaphores           map[string]chan struct{}
	store                store.ExecutionStore
	localExecutor        executor.Executor
	sshExecutor          executor.Executor
	defaultExecutionMode string
	defaultTimeout       time.Duration
}

func NewService(
	logger *slog.Logger,
	cfg *config.Config,
	localExecutor executor.Executor,
	sshExecutor executor.Executor,
	executionStore store.ExecutionStore,
	defaultExecutionMode string,
	defaultTimeout time.Duration,
) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if executionStore == nil {
		return nil, errors.New("execution store is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	service := &Service{
		logger:               logger,
		cfg:                  cfg,
		hooksByID:            make(map[string]config.HookConfig, len(cfg.Hooks)),
		hooksByPath:          make(map[string]config.HookConfig, len(cfg.Hooks)),
		semaphores:           make(map[string]chan struct{}, len(cfg.Hooks)),
		store:                executionStore,
		localExecutor:        localExecutor,
		sshExecutor:          sshExecutor,
		defaultExecutionMode: strings.ToLower(strings.TrimSpace(defaultExecutionMode)),
		defaultTimeout:       defaultTimeout,
	}
	if service.defaultExecutionMode == "" {
		service.defaultExecutionMode = "local"
	}
	if service.defaultTimeout <= 0 {
		service.defaultTimeout = time.Duration(cfg.Global.RequestTimeoutSeconds) * time.Second
	}

	for _, hook := range cfg.Hooks {
		service.hooksByID[hook.ID] = hook
		service.hooksByPath[hook.Path] = hook
		service.semaphores[hook.ID] = make(chan struct{}, cfg.Global.MaxConcurrentJobsPerHook)
	}

	return service, nil
}

func (s *Service) HandleHookByID(w http.ResponseWriter, r *http.Request) {
	hookID := strings.TrimSpace(chi.URLParam(r, "hookId"))
	hook, ok := s.hooksByID[hookID]
	if !ok || !hook.IsEnabled() {
		writeJSONError(w, http.StatusNotFound, "hook not found or disabled")
		return
	}
	s.handleHook(w, r, hook)
}

func (s *Service) HandleHookByPath(w http.ResponseWriter, r *http.Request) {
	hook, ok := s.hooksByPath[r.URL.Path]
	if !ok || !hook.IsEnabled() {
		writeJSONError(w, http.StatusNotFound, "hook not found or disabled")
		return
	}
	s.handleHook(w, r, hook)
}

func (s *Service) handleHook(w http.ResponseWriter, r *http.Request, hook config.HookConfig) {
	requestID := getRequestID(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	eventType := security.ExtractEventType(hook.Provider, r.Header)
	if !isAllowedEvent(hook.EventTypes, eventType) {
		writeJSONError(w, http.StatusBadRequest, "event type is not allowed")
		return
	}

	secret := strings.TrimSpace(os.Getenv(hook.SecretEnv))
	if secret == "" {
		s.logger.Error("hook secret is empty", "hook_id", hook.ID, "secret_env", hook.SecretEnv)
		writeJSONError(w, http.StatusInternalServerError, "hook secret is not configured")
		return
	}
	if err := security.Verify(hook.Provider, secret, body, r.Header); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	sem := s.semaphores[hook.ID]
	if !s.acquireSlot(sem, r.Context()) {
		writeJSONError(w, http.StatusConflict, "hook is busy")
		return
	}

	commands := s.resolveCommands(hook)
	if len(commands) == 0 {
		<-sem
		writeJSONError(w, http.StatusInternalServerError, "no executable command found")
		return
	}

	mode := s.resolveExecutionMode(hook)
	execImpl, err := s.getExecutor(mode)
	if err != nil {
		<-sem
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	timeout := s.resolveTimeout(hook)
	branch, commitSHA := extractGitMetadata(body)

	record := model.ExecutionRecord{
		RequestID: requestID,
		HookID:    hook.ID,
		Provider:  hook.Provider,
		EventType: eventType,
		Status:    model.ExecutionStatusQueued,
		SourceIP:  getSourceIP(r),
		Branch:    branch,
		CommitSHA: commitSHA,
		StartedAt: time.Now(),
	}
	s.store.Save(record)

	go s.runHookExecution(record, hook, sem, execImpl, mode, timeout, commands)

	response := map[string]string{
		"request_id": requestID,
		"hook_id":    hook.ID,
		"status":     "accepted",
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Service) runHookExecution(
	record model.ExecutionRecord,
	hook config.HookConfig,
	sem chan struct{},
	execImpl executor.Executor,
	mode string,
	timeout time.Duration,
	commands []string,
) {
	defer func() {
		<-sem
	}()

	startedAt := time.Now()
	record.Status = model.ExecutionStatusRunning
	record.StartedAt = startedAt
	s.store.Update(record)

	execRequest := executor.Request{
		HookID:   hook.ID,
		Commands: commands,
	}
	if mode == "ssh" {
		profile := s.cfg.SSHProfiles[hook.SSHProfile]
		execRequest.SSHProfile = &profile
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := execImpl.Execute(ctx, execRequest)
	finishedAt := time.Now()

	record.FinishedAt = finishedAt
	record.DurationMS = finishedAt.Sub(startedAt).Milliseconds()
	record.ExitCode = result.ExitCode
	record.StepResults = result.Steps

	if err != nil {
		record.Status = model.ExecutionStatusFailed
		record.ErrorMessage = err.Error()
		s.logger.Error(
			"hook execution failed",
			"request_id", record.RequestID,
			"hook_id", record.HookID,
			"error", err.Error(),
			"duration_ms", record.DurationMS,
		)
	} else {
		record.Status = model.ExecutionStatusSuccess
		s.logger.Info(
			"hook execution success",
			"request_id", record.RequestID,
			"hook_id", record.HookID,
			"duration_ms", record.DurationMS,
		)
	}

	s.store.Update(record)
}

func (s *Service) acquireSlot(sem chan struct{}, reqCtx context.Context) bool {
	if s.cfg.Global.RejectWhenBusy {
		select {
		case sem <- struct{}{}:
			return true
		default:
			return false
		}
	}

	select {
	case sem <- struct{}{}:
		return true
	case <-reqCtx.Done():
		return false
	}
}

func (s *Service) resolveCommands(hook config.HookConfig) []string {
	commands := make([]string, 0, 8)
	for _, groupName := range hook.CommandGroups {
		group := s.cfg.CommandGroups[groupName]
		commands = append(commands, group.Steps...)
	}
	return commands
}

func (s *Service) resolveExecutionMode(hook config.HookConfig) string {
	if hook.ExecutionMode != "" {
		return hook.ExecutionMode
	}
	if s.defaultExecutionMode != "" {
		return s.defaultExecutionMode
	}
	return "local"
}

func (s *Service) resolveTimeout(hook config.HookConfig) time.Duration {
	if hook.TimeoutSeconds > 0 {
		return time.Duration(hook.TimeoutSeconds) * time.Second
	}
	if s.cfg.Global.RequestTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Global.RequestTimeoutSeconds) * time.Second
	}
	if s.defaultTimeout > 0 {
		return s.defaultTimeout
	}
	return 60 * time.Second
}

func (s *Service) getExecutor(mode string) (executor.Executor, error) {
	switch mode {
	case "local":
		if s.localExecutor == nil {
			return nil, errors.New("local executor is not configured")
		}
		return s.localExecutor, nil
	case "ssh":
		if s.sshExecutor == nil {
			return nil, errors.New("ssh executor is not configured")
		}
		return s.sshExecutor, nil
	default:
		return nil, fmt.Errorf("unsupported execution mode: %s", mode)
	}
}

func isAllowedEvent(allowed []string, eventType string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalizedEvent := strings.ToLower(strings.TrimSpace(eventType))
	for _, item := range allowed {
		if normalizedEvent == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func extractGitMetadata(body []byte) (string, string) {
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		HeadCommit struct {
			ID string `json:"id"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	branch := strings.TrimPrefix(strings.TrimSpace(payload.Ref), "refs/heads/")
	commit := strings.TrimSpace(payload.After)
	if commit == "" {
		commit = strings.TrimSpace(payload.HeadCommit.ID)
	}
	return branch, commit
}

func getSourceIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func getRequestID(r *http.Request) string {
	if reqID := strings.TrimSpace(r.Header.Get("X-Request-Id")); reqID != "" {
		return reqID
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
