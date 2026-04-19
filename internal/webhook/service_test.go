package webhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"webhook-docker/internal/config"
	"webhook-docker/internal/executor"
	"webhook-docker/internal/router"
	"webhook-docker/internal/store"
	"webhook-docker/internal/webhook"
)

type fakeExecutor struct {
	called chan executor.Request
	block  chan struct{}
	err    error
}

func (f *fakeExecutor) Execute(ctx context.Context, req executor.Request) (executor.Result, error) {
	if f.called != nil {
		select {
		case f.called <- req:
		default:
		}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return executor.Result{ExitCode: 124}, ctx.Err()
		}
	}
	if f.err != nil {
		return executor.Result{ExitCode: 1}, f.err
	}
	return executor.Result{ExitCode: 0}, nil
}

func TestHookAcceptedByID(t *testing.T) {
	handler, localExec := buildTestHandler(t, "/hooks/blog-update", true)
	t.Setenv("TEST_SECRET", "abc123")

	body := []byte(`{"ref":"refs/heads/main","after":"1234"}`)
	req := newGitHubRequest("/hooks/blog-update", body, "abc123", "push")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d, body=%s", rr.Code, rr.Body.String())
	}

	select {
	case <-localExec.called:
	case <-time.After(2 * time.Second):
		t.Fatal("executor was not called")
	}
}

func TestHookRejectedWhenSignatureInvalid(t *testing.T) {
	handler, _ := buildTestHandler(t, "/hooks/blog-update", true)
	t.Setenv("TEST_SECRET", "abc123")

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/blog-update", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	req.Header.Set("X-GitHub-Event", "push")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHookRejectWhenBusy(t *testing.T) {
	handler, localExec := buildTestHandler(t, "/hooks/blog-update", true)
	t.Setenv("TEST_SECRET", "abc123")

	block := make(chan struct{})
	localExec.block = block

	body := []byte(`{"ref":"refs/heads/main"}`)
	firstReq := newGitHubRequest("/hooks/blog-update", body, "abc123", "push")
	firstRR := httptest.NewRecorder()
	handler.ServeHTTP(firstRR, firstReq)
	if firstRR.Code != http.StatusAccepted {
		t.Fatalf("first request expected 202, got %d", firstRR.Code)
	}

	select {
	case <-localExec.called:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not reach executor")
	}

	secondReq := newGitHubRequest("/hooks/blog-update", body, "abc123", "push")
	secondRR := httptest.NewRecorder()
	handler.ServeHTTP(secondRR, secondReq)
	if secondRR.Code != http.StatusConflict {
		t.Fatalf("second request expected 409, got %d", secondRR.Code)
	}

	close(block)
}

func TestHookAcceptedByPath(t *testing.T) {
	handler, _ := buildTestHandler(t, "/custom/deploy", true)
	t.Setenv("TEST_SECRET", "abc123")

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := newGitHubRequest("/custom/deploy", body, "abc123", "push")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202 by custom path, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestHookNotFound(t *testing.T) {
	handler, _ := buildTestHandler(t, "/hooks/blog-update", true)
	t.Setenv("TEST_SECRET", "abc123")

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := newGitHubRequest("/hooks/not-exist", body, "abc123", "push")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func buildTestHandler(t *testing.T, path string, rejectWhenBusy bool) (http.Handler, *fakeExecutor) {
	t.Helper()
	enabled := true

	cfg := &config.Config{
		Global: config.GlobalConfig{
			RequestTimeoutSeconds:    5,
			MaxConcurrentJobsPerHook: 1,
			RejectWhenBusy:           rejectWhenBusy,
		},
		Hooks: []config.HookConfig{
			{
				ID:            "blog-update",
				Path:          path,
				Provider:      "github",
				SecretEnv:     "TEST_SECRET",
				Enabled:       &enabled,
				EventTypes:    []string{"push"},
				ExecutionMode: "local",
				CommandGroups: []string{"update"},
			},
		},
		CommandGroups: map[string]config.CommandGroup{
			"update": {Steps: []string{"echo test"}},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validate failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	localExec := &fakeExecutor{called: make(chan executor.Request, 8)}
	sshExec := &fakeExecutor{err: errors.New("ssh not expected")}

	svc, err := webhook.NewService(
		logger,
		cfg,
		localExec,
		sshExec,
		store.NewMemoryStore(),
		"local",
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("new service failed: %v", err)
	}

	return router.New(svc), localExec
}

func newGitHubRequest(path string, body []byte, secret, eventType string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+buildSignature(secret, body))
	req.Header.Set("X-GitHub-Event", eventType)
	return req
}

func buildSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
