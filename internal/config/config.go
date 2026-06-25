package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	WorkspaceRoot         string
	StateDir              string
	ActiveProject         string
	CommandPolicy         string
	AllowedCommands       map[string]struct{}
	Shell                 string
	AllowDelete           bool
	MaxFileBytes          int64
	MaxSearchResults      int
	MaxTreeEntries        int
	MaxConcurrentProcess  int
	MaxProcessOutput      int
	DefaultProcessTimeout time.Duration
	ForegroundYield       time.Duration
}

func Load() (Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("get current directory: %w", err)
	}
	root := env("CODEFORGE_WORKSPACE_ROOT", cwd)
	root, err = filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return Config{}, fmt.Errorf("workspace root: %w", err)
	}
	if !info.IsDir() {
		return Config{}, errors.New("CODEFORGE_WORKSPACE_ROOT must be a directory")
	}

	policy := strings.ToLower(env("CODEFORGE_COMMAND_POLICY", "unrestricted"))
	if policy != "unrestricted" && policy != "allowlist" {
		return Config{}, errors.New("CODEFORGE_COMMAND_POLICY must be unrestricted or allowlist")
	}

	yieldMS := envInt("CODEFORGE_FOREGROUND_YIELD_MS", 10_000)
	if yieldMS > 30_000 {
		return Config{}, errors.New("CODEFORGE_FOREGROUND_YIELD_MS cannot exceed 30000")
	}

	stateDir := strings.TrimSpace(os.Getenv("CODEFORGE_STATE_DIR"))
	if stateDir == "" {
		stateDir, err = defaultStateDir(root)
		if err != nil {
			return Config{}, err
		}
	} else {
		stateDir, err = filepath.Abs(stateDir)
		if err != nil {
			return Config{}, fmt.Errorf("resolve state directory: %w", err)
		}
	}

	return Config{
		WorkspaceRoot:         root,
		StateDir:              stateDir,
		ActiveProject:         strings.TrimSpace(os.Getenv("CODEFORGE_ACTIVE_PROJECT")),
		CommandPolicy:         policy,
		AllowedCommands:       commandSet(env("CODEFORGE_ALLOWED_COMMANDS", "go,git,rg,gofmt,goimports,golangci-lint,staticcheck,make,just,cargo,rustc,npm,npx,pnpm,yarn,node,python,python3,pytest")),
		Shell:                 env("CODEFORGE_SHELL", defaultShell()),
		AllowDelete:           envBool("CODEFORGE_ALLOW_DELETE", true),
		MaxFileBytes:          int64(envInt("CODEFORGE_MAX_FILE_BYTES", 4<<20)),
		MaxSearchResults:      envInt("CODEFORGE_MAX_SEARCH_RESULTS", 300),
		MaxTreeEntries:        envInt("CODEFORGE_MAX_TREE_ENTRIES", 3000),
		MaxConcurrentProcess:  envInt("CODEFORGE_MAX_CONCURRENT_PROCESSES", 4),
		MaxProcessOutput:      envInt("CODEFORGE_MAX_PROCESS_OUTPUT_BYTES", 8<<20),
		DefaultProcessTimeout: time.Duration(envInt("CODEFORGE_PROCESS_TIMEOUT_SECONDS", 600)) * time.Second,
		ForegroundYield:       time.Duration(yieldMS) * time.Millisecond,
	}, nil
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/bash"
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func commandSet(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, command := range splitCSV(value) {
		result[command] = struct{}{}
	}
	return result
}

func defaultStateDir(workspaceRoot string) (string, error) {
	sum := sha256.Sum256([]byte(workspaceRoot))
	workspaceID := fmt.Sprintf("%x", sum[:8])
	if base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); base != "" {
		return filepath.Join(base, "codeforge-mcp", workspaceID), nil
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "codeforge-mcp", workspaceID), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve default state directory: %w", err)
	}
	return filepath.Join(base, "codeforge-mcp", "state", workspaceID), nil
}
