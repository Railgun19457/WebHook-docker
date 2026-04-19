package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	envBindAddr              = "WEBHOOK_BIND_ADDR"
	envBindPort              = "WEBHOOK_BIND_PORT"
	envLogLevel              = "WEBHOOK_LOG_LEVEL"
	envConfigPath            = "WEBHOOK_CONFIG_PATH"
	envDefaultTimeoutSeconds = "WEBHOOK_DEFAULT_TIMEOUT_SECONDS"
	envExecutionMode         = "WEBHOOK_EXECUTION_MODE"
	envSSHPrivateKeyPath     = "SSH_PRIVATE_KEY_PATH"
	envSSHPassphrase         = "SSH_PASSPHRASE"
	envSSHKnownHostsPath     = "SSH_KNOWN_HOSTS_PATH"
)

type AppEnv struct {
	BindAddr              string
	BindPort              int
	LogLevel              slog.Level
	ConfigPath            string
	DefaultTimeoutSeconds int
	DefaultExecutionMode  string
	SSHPrivateKeyPath     string
	SSHPassphrase         string
	SSHKnownHostsPath     string
}

func LoadAppEnv() (AppEnv, error) {
	bindPort, err := getIntEnv(envBindPort, 8080)
	if err != nil {
		return AppEnv{}, err
	}
	defaultTimeout, err := getIntEnv(envDefaultTimeoutSeconds, 60)
	if err != nil {
		return AppEnv{}, err
	}
	if defaultTimeout <= 0 {
		return AppEnv{}, fmt.Errorf("%s must be greater than 0", envDefaultTimeoutSeconds)
	}

	logLevel, err := parseLogLevel(getEnvOrDefault(envLogLevel, "info"))
	if err != nil {
		return AppEnv{}, err
	}

	mode := strings.ToLower(strings.TrimSpace(getEnvOrDefault(envExecutionMode, "local")))
	if mode != "local" && mode != "ssh" {
		return AppEnv{}, fmt.Errorf("%s must be local or ssh", envExecutionMode)
	}

	return AppEnv{
		BindAddr:              getEnvOrDefault(envBindAddr, "0.0.0.0"),
		BindPort:              bindPort,
		LogLevel:              logLevel,
		ConfigPath:            getEnvOrDefault(envConfigPath, "configs/webhook.yaml"),
		DefaultTimeoutSeconds: defaultTimeout,
		DefaultExecutionMode:  mode,
		SSHPrivateKeyPath:     strings.TrimSpace(os.Getenv(envSSHPrivateKeyPath)),
		SSHPassphrase:         os.Getenv(envSSHPassphrase),
		SSHKnownHostsPath:     strings.TrimSpace(os.Getenv(envSSHKnownHostsPath)),
	}, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level: %s", raw)
	}
}

func getEnvOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func getIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %w", key, err)
	}
	return value, nil
}

type Config struct {
	Global        GlobalConfig            `yaml:"global"`
	Hooks         []HookConfig            `yaml:"hooks"`
	CommandGroups map[string]CommandGroup `yaml:"commandGroups"`
	SSHProfiles   map[string]SSHProfile   `yaml:"sshProfiles"`
}

type GlobalConfig struct {
	RequestTimeoutSeconds    int  `yaml:"requestTimeoutSeconds"`
	MaxConcurrentJobsPerHook int  `yaml:"maxConcurrentJobsPerHook"`
	RejectWhenBusy           bool `yaml:"rejectWhenBusy"`
}

type HookConfig struct {
	ID             string   `yaml:"id"`
	Path           string   `yaml:"path"`
	Provider       string   `yaml:"provider"`
	SecretEnv      string   `yaml:"secretEnv"`
	Enabled        *bool    `yaml:"enabled"`
	EventTypes     []string `yaml:"eventTypes"`
	ExecutionMode  string   `yaml:"executionMode"`
	SSHProfile     string   `yaml:"sshProfile"`
	CommandGroups  []string `yaml:"commandGroups"`
	TimeoutSeconds int      `yaml:"timeoutSeconds"`
}

func (h HookConfig) IsEnabled() bool {
	if h.Enabled == nil {
		return true
	}
	return *h.Enabled
}

type CommandGroup struct {
	Steps []string `yaml:"steps"`
}

type SSHProfile struct {
	Host                  string  `yaml:"host"`
	Port                  int     `yaml:"port"`
	Username              string  `yaml:"username"`
	StrictHostKeyChecking bool    `yaml:"strictHostKeyChecking"`
	KnownHostsPath        string  `yaml:"knownHostsPath"`
	Auth                  SSHAuth `yaml:"auth"`
}

type SSHAuth struct {
	Method         string `yaml:"method"`
	PrivateKeyPath string `yaml:"privateKeyPath"`
	PassphraseEnv  string `yaml:"passphraseEnv"`
	PasswordEnv    string `yaml:"passwordEnv"`
}

func LoadFromFile(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) normalize() {
	if c.Global.RequestTimeoutSeconds <= 0 {
		c.Global.RequestTimeoutSeconds = 60
	}
	if c.Global.MaxConcurrentJobsPerHook <= 0 {
		c.Global.MaxConcurrentJobsPerHook = 1
	}

	normalizedGroups := make(map[string]CommandGroup, len(c.CommandGroups))
	for key, group := range c.CommandGroups {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		group.Steps = trimList(group.Steps)
		normalizedGroups[trimmedKey] = group
	}
	c.CommandGroups = normalizedGroups

	normalizedProfiles := make(map[string]SSHProfile, len(c.SSHProfiles))
	for key, profile := range c.SSHProfiles {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		profile.Host = strings.TrimSpace(profile.Host)
		profile.Username = strings.TrimSpace(profile.Username)
		if profile.Port <= 0 {
			profile.Port = 22
		}
		profile.Auth.Method = strings.ToLower(strings.TrimSpace(profile.Auth.Method))
		if profile.Auth.Method == "" {
			profile.Auth.Method = "key"
		}
		profile.Auth.PrivateKeyPath = strings.TrimSpace(profile.Auth.PrivateKeyPath)
		profile.Auth.PassphraseEnv = strings.TrimSpace(profile.Auth.PassphraseEnv)
		profile.Auth.PasswordEnv = strings.TrimSpace(profile.Auth.PasswordEnv)
		profile.KnownHostsPath = strings.TrimSpace(profile.KnownHostsPath)
		normalizedProfiles[trimmedKey] = profile
	}
	c.SSHProfiles = normalizedProfiles

	for idx := range c.Hooks {
		hook := &c.Hooks[idx]
		hook.ID = strings.TrimSpace(hook.ID)
		hook.Path = strings.TrimSpace(hook.Path)
		hook.Provider = strings.ToLower(strings.TrimSpace(hook.Provider))
		if hook.Provider == "" {
			hook.Provider = "github"
		}
		hook.SecretEnv = strings.TrimSpace(hook.SecretEnv)
		hook.ExecutionMode = strings.ToLower(strings.TrimSpace(hook.ExecutionMode))
		hook.SSHProfile = strings.TrimSpace(hook.SSHProfile)
		hook.CommandGroups = trimList(hook.CommandGroups)
		hook.EventTypes = normalizeLowerList(hook.EventTypes)

		if hook.ExecutionMode == "" {
			hook.ExecutionMode = "local"
		}
		if hook.Path == "" && hook.ID != "" {
			hook.Path = "/hooks/" + hook.ID
		}
		if hook.Path != "" && !strings.HasPrefix(hook.Path, "/") {
			hook.Path = "/" + hook.Path
		}
	}
}

func (c *Config) Validate() error {
	if len(c.Hooks) == 0 {
		return errors.New("at least one hook is required")
	}
	if len(c.CommandGroups) == 0 {
		return errors.New("at least one command group is required")
	}
	if c.Global.RequestTimeoutSeconds <= 0 {
		return errors.New("global.requestTimeoutSeconds must be greater than 0")
	}
	if c.Global.MaxConcurrentJobsPerHook <= 0 {
		return errors.New("global.maxConcurrentJobsPerHook must be greater than 0")
	}

	for name, group := range c.CommandGroups {
		if len(group.Steps) == 0 {
			return fmt.Errorf("command group %s must include at least one step", name)
		}
		for _, step := range group.Steps {
			if strings.TrimSpace(step) == "" {
				return fmt.Errorf("command group %s contains an empty step", name)
			}
		}
	}

	idSeen := make(map[string]struct{}, len(c.Hooks))
	pathSeen := make(map[string]struct{}, len(c.Hooks))
	for _, hook := range c.Hooks {
		if hook.ID == "" {
			return errors.New("hook id must not be empty")
		}
		if _, exists := idSeen[hook.ID]; exists {
			return fmt.Errorf("hook id duplicated: %s", hook.ID)
		}
		idSeen[hook.ID] = struct{}{}

		if hook.Path == "" {
			return fmt.Errorf("hook %s path must not be empty", hook.ID)
		}
		if _, exists := pathSeen[hook.Path]; exists {
			return fmt.Errorf("hook path duplicated: %s", hook.Path)
		}
		pathSeen[hook.Path] = struct{}{}

		if hook.SecretEnv == "" {
			return fmt.Errorf("hook %s secretEnv must not be empty", hook.ID)
		}

		if !isSupportedProvider(hook.Provider) {
			return fmt.Errorf("hook %s provider %s is not supported", hook.ID, hook.Provider)
		}

		if hook.ExecutionMode != "local" && hook.ExecutionMode != "ssh" {
			return fmt.Errorf("hook %s executionMode must be local or ssh", hook.ID)
		}

		if len(hook.CommandGroups) == 0 {
			return fmt.Errorf("hook %s commandGroups must not be empty", hook.ID)
		}
		for _, name := range hook.CommandGroups {
			if _, ok := c.CommandGroups[name]; !ok {
				return fmt.Errorf("hook %s references unknown command group %s", hook.ID, name)
			}
		}

		if hook.ExecutionMode == "ssh" {
			if hook.SSHProfile == "" {
				return fmt.Errorf("hook %s requires sshProfile when executionMode=ssh", hook.ID)
			}
			if _, ok := c.SSHProfiles[hook.SSHProfile]; !ok {
				return fmt.Errorf("hook %s references unknown sshProfile %s", hook.ID, hook.SSHProfile)
			}
		}

		if hook.SSHProfile != "" {
			if _, ok := c.SSHProfiles[hook.SSHProfile]; !ok {
				return fmt.Errorf("hook %s references unknown sshProfile %s", hook.ID, hook.SSHProfile)
			}
		}

		if hook.TimeoutSeconds < 0 {
			return fmt.Errorf("hook %s timeoutSeconds must not be negative", hook.ID)
		}
	}

	for name, profile := range c.SSHProfiles {
		if profile.Host == "" {
			return fmt.Errorf("sshProfile %s host must not be empty", name)
		}
		if profile.Username == "" {
			return fmt.Errorf("sshProfile %s username must not be empty", name)
		}
		switch profile.Auth.Method {
		case "key":
			if profile.Auth.PrivateKeyPath == "" {
				// allow fallback from environment.
			}
		case "password":
			if profile.Auth.PasswordEnv == "" {
				return fmt.Errorf("sshProfile %s auth.passwordEnv must not be empty", name)
			}
		default:
			return fmt.Errorf("sshProfile %s auth.method must be key or password", name)
		}
	}

	return nil
}

func isSupportedProvider(provider string) bool {
	switch provider {
	case "github", "gitea", "generic":
		return true
	default:
		return false
	}
}

func trimList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeLowerList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
