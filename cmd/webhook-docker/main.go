package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"webhook-docker/internal/config"
	"webhook-docker/internal/executor"
	"webhook-docker/internal/router"
	"webhook-docker/internal/store"
	"webhook-docker/internal/webhook"
)

func main() {
	envCfg, err := config.LoadAppEnv()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load env failed: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(envCfg.LogLevel)

	appCfg, err := config.LoadFromFile(envCfg.ConfigPath)
	if err != nil {
		logger.Error("load app config failed", "error", err.Error())
		os.Exit(1)
	}

	executionStore := store.NewMemoryStore()
	localExecutor := executor.NewLocalExecutor(logger)
	sshExecutor := executor.NewSSHExecutor(
		logger,
		envCfg.SSHPrivateKeyPath,
		envCfg.SSHPassphrase,
		envCfg.SSHKnownHostsPath,
	)

	service, err := webhook.NewService(
		logger,
		appCfg,
		localExecutor,
		sshExecutor,
		executionStore,
		envCfg.DefaultExecutionMode,
		time.Duration(envCfg.DefaultTimeoutSeconds)*time.Second,
	)
	if err != nil {
		logger.Error("build service failed", "error", err.Error())
		os.Exit(1)
	}

	handler := router.New(service)
	addr := fmt.Sprintf("%s:%d", envCfg.BindAddr, envCfg.BindPort)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("webhook-docker started", "addr", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server exited", "error", err.Error())
		os.Exit(1)
	}
}

func newLogger(level slog.Level) *slog.Logger {
	handlerOptions := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(os.Stdout, handlerOptions)
	return slog.New(handler)
}
