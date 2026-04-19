package executor

import (
	"context"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"webhook-docker/internal/config"
	"webhook-docker/internal/model"
)

type SSHExecutor struct {
	logger                *slog.Logger
	defaultPrivateKeyPath string
	defaultPassphrase     string
	defaultKnownHostsPath string
	dialTimeout           time.Duration
}

func NewSSHExecutor(logger *slog.Logger, defaultPrivateKeyPath, defaultPassphrase, defaultKnownHostsPath string) *SSHExecutor {
	return &SSHExecutor{
		logger:                logger,
		defaultPrivateKeyPath: strings.TrimSpace(defaultPrivateKeyPath),
		defaultPassphrase:     defaultPassphrase,
		defaultKnownHostsPath: strings.TrimSpace(defaultKnownHostsPath),
		dialTimeout:           10 * time.Second,
	}
}

func (e *SSHExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	if req.SSHProfile == nil {
		return Result{}, errors.New("ssh profile is required")
	}
	if len(req.Commands) == 0 {
		return Result{ExitCode: 0}, nil
	}

	profile := *req.SSHProfile
	clientConfig, err := e.buildClientConfig(profile)
	if err != nil {
		return Result{}, err
	}

	addr := fmt.Sprintf("%s:%d", profile.Host, profile.Port)
	client, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return Result{}, fmt.Errorf("ssh dial failed: %w", err)
	}
	defer client.Close()

	result := Result{Steps: make([]model.StepResult, 0, len(req.Commands))}
	for _, commandLine := range req.Commands {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		step, runErr := e.runCommand(ctx, client, commandLine)
		result.Steps = append(result.Steps, step)
		if runErr != nil {
			result.ExitCode = step.ExitCode
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, runErr
		}
	}

	result.ExitCode = 0
	return result, nil
}

func (e *SSHExecutor) runCommand(ctx context.Context, client *ssh.Client, commandLine string) (model.StepResult, error) {
	session, err := client.NewSession()
	if err != nil {
		return model.StepResult{Command: commandLine, ExitCode: 1, ErrorMessage: err.Error()}, fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	startedAt := time.Now()
	type cmdResult struct {
		output []byte
		err    error
	}
	done := make(chan cmdResult, 1)
	go func() {
		output, runErr := session.CombinedOutput(commandLine)
		done <- cmdResult{output: output, err: runErr}
	}()

	step := model.StepResult{Command: commandLine, StartedAt: startedAt}

	select {
	case <-ctx.Done():
		_ = session.Close()
		finishedAt := time.Now()
		step.FinishedAt = finishedAt
		step.DurationMS = finishedAt.Sub(startedAt).Milliseconds()
		step.ExitCode = 124
		step.ErrorMessage = ctx.Err().Error()
		return step, ctx.Err()
	case res := <-done:
		finishedAt := time.Now()
		step.FinishedAt = finishedAt
		step.DurationMS = finishedAt.Sub(startedAt).Milliseconds()
		step.Stdout = strings.TrimSpace(string(res.output))
		if res.err != nil {
			step.ErrorMessage = res.err.Error()
			if exitErr, ok := res.err.(*ssh.ExitError); ok {
				step.ExitCode = exitErr.ExitStatus()
			} else {
				step.ExitCode = 1
			}
			return step, fmt.Errorf("ssh command failed: %w", res.err)
		}
		step.ExitCode = 0
		if e.logger != nil {
			e.logger.Debug("ssh command executed", "command", commandLine, "duration_ms", step.DurationMS)
		}
		return step, nil
	}
}

func (e *SSHExecutor) buildClientConfig(profile config.SSHProfile) (*ssh.ClientConfig, error) {
	authMethods, err := e.buildAuthMethods(profile.Auth)
	if err != nil {
		return nil, err
	}

	hostKeyCallback, err := e.buildHostKeyCallback(profile)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            profile.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         e.dialTimeout,
	}, nil
}

func (e *SSHExecutor) buildAuthMethods(auth config.SSHAuth) ([]ssh.AuthMethod, error) {
	method := strings.ToLower(strings.TrimSpace(auth.Method))
	if method == "" {
		method = "key"
	}

	switch method {
	case "key":
		privateKeyPath := strings.TrimSpace(auth.PrivateKeyPath)
		if privateKeyPath == "" {
			privateKeyPath = e.defaultPrivateKeyPath
		}
		if privateKeyPath == "" {
			return nil, errors.New("ssh private key path is empty")
		}

		privateKey, err := os.ReadFile(privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read ssh private key: %w", err)
		}

		passphrase := e.defaultPassphrase
		if auth.PassphraseEnv != "" {
			passphrase = os.Getenv(auth.PassphraseEnv)
		}

		signer, err := parseSigner(privateKey, passphrase)
		if err != nil {
			return nil, err
		}

		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case "password":
		if auth.PasswordEnv == "" {
			return nil, errors.New("ssh password env is empty")
		}
		password := os.Getenv(auth.PasswordEnv)
		if password == "" {
			return nil, fmt.Errorf("ssh password env %s is empty", auth.PasswordEnv)
		}
		return []ssh.AuthMethod{ssh.Password(password)}, nil
	default:
		return nil, fmt.Errorf("unsupported ssh auth method: %s", method)
	}
}

func (e *SSHExecutor) buildHostKeyCallback(profile config.SSHProfile) (ssh.HostKeyCallback, error) {
	if !profile.StrictHostKeyChecking {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	knownHostsPath := strings.TrimSpace(profile.KnownHostsPath)
	if knownHostsPath == "" {
		knownHostsPath = e.defaultKnownHostsPath
	}
	if knownHostsPath == "" {
		return nil, errors.New("known hosts path is empty while strictHostKeyChecking=true")
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load known hosts: %w", err)
	}
	return callback, nil
}

func parseSigner(privateKey []byte, passphrase string) (ssh.Signer, error) {
	if strings.TrimSpace(passphrase) == "" {
		signer, err := ssh.ParsePrivateKey(privateKey)
		if err == nil {
			return signer, nil
		}
		if _, ok := err.(*ssh.PassphraseMissingError); !ok {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
	}

	signer, err := ssh.ParsePrivateKeyWithPassphrase(privateKey, []byte(passphrase))
	if err != nil {
		if pemBlock, _ := pem.Decode(privateKey); pemBlock == nil {
			return nil, fmt.Errorf("invalid private key format: %w", err)
		}
		return nil, fmt.Errorf("parse encrypted private key: %w", err)
	}
	return signer, nil
}

func newRandomBytes(size int) []byte {
	buf := make([]byte, size)
	_, _ = rand.Read(buf)
	return buf
}
