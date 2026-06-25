package config

import (
	"runtime"
	"testing"
	"time"
)

func TestLoadStdioConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEFORGE_WORKSPACE_ROOT", root)
	t.Setenv("CODEFORGE_ACTIVE_PROJECT", "demo")
	stateDir := t.TempDir()
	t.Setenv("CODEFORGE_STATE_DIR", stateDir)
	t.Setenv("CODEFORGE_COMMAND_POLICY", "unrestricted")
	t.Setenv("CODEFORGE_FOREGROUND_YIELD_MS", "2500")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceRoot != root || cfg.StateDir != stateDir || cfg.ActiveProject != "demo" || cfg.CommandPolicy != "unrestricted" || !cfg.AllowDelete {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.ForegroundYield != 2500*time.Millisecond {
		t.Fatalf("unexpected foreground yield: %v", cfg.ForegroundYield)
	}
	if runtime.GOOS != "windows" && cfg.Shell == "" {
		t.Fatal("default shell missing")
	}
}

func TestLoadRejectsUnknownCommandPolicy(t *testing.T) {
	t.Setenv("CODEFORGE_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("CODEFORGE_COMMAND_POLICY", "magic")
	if _, err := Load(); err == nil {
		t.Fatal("expected policy error")
	}
}

func TestLoadRejectsExcessiveForegroundYield(t *testing.T) {
	t.Setenv("CODEFORGE_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("CODEFORGE_FOREGROUND_YIELD_MS", "30001")
	if _, err := Load(); err == nil {
		t.Fatal("expected foreground yield error")
	}
}

func TestLoadDerivesStableDefaultStateDirectory(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CODEFORGE_WORKSPACE_ROOT", root)
	t.Setenv("CODEFORGE_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", home)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if first.StateDir != second.StateDir || first.StateDir == home {
		t.Fatalf("unexpected derived state directory: %q %q", first.StateDir, second.StateDir)
	}
}
