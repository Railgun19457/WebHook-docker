package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromFileAndValidateSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "webhook.yaml")

	content := `global:
  requestTimeoutSeconds: 60
  maxConcurrentJobsPerHook: 1
  rejectWhenBusy: true
hooks:
  - id: blog-update
    path: /hooks/blog-update
    provider: github
    secretEnv: GITHUB_BLOG_WEBHOOK_SECRET
    enabled: true
    eventTypes: [push]
    executionMode: local
    commandGroups:
      - update1
commandGroups:
  update1:
    steps:
      - echo hello
`

	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(cfg.Hooks))
	}
}

func TestValidateRejectUnknownCommandGroup(t *testing.T) {
	enabled := true
	cfg := &Config{
		Global: GlobalConfig{
			RequestTimeoutSeconds:    30,
			MaxConcurrentJobsPerHook: 1,
			RejectWhenBusy:           true,
		},
		Hooks: []HookConfig{
			{
				ID:            "deploy",
				Path:          "/hooks/deploy",
				Provider:      "github",
				SecretEnv:     "TEST_SECRET",
				Enabled:       &enabled,
				ExecutionMode: "local",
				CommandGroups: []string{"missing"},
			},
		},
		CommandGroups: map[string]CommandGroup{
			"valid": {Steps: []string{"echo ok"}},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	if !strings.Contains(err.Error(), "unknown command group") {
		t.Fatalf("expected unknown command group error, got: %v", err)
	}
}
