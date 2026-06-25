package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/killercrock/codeforge-mcp/internal/config"
	"github.com/killercrock/codeforge-mcp/internal/mcp"
	"github.com/killercrock/codeforge-mcp/internal/plan"
	proc "github.com/killercrock/codeforge-mcp/internal/process"
	"github.com/killercrock/codeforge-mcp/internal/project"
	"github.com/killercrock/codeforge-mcp/internal/tools"
)

const version = "0.6.1"

func main() {
	if versionRequested(os.Args[1:]) {
		fmt.Println(version)
		return
	}
	os.Exit(run())
}

func versionRequested(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "version")
}

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		return 2
	}

	projects, err := project.NewManager(cfg.WorkspaceRoot, cfg.ActiveProject, project.Limits{
		MaxFileBytes: cfg.MaxFileBytes, MaxSearchResults: cfg.MaxSearchResults,
		MaxTreeEntries: cfg.MaxTreeEntries, AllowDelete: cfg.AllowDelete,
	})
	if err != nil {
		logger.Error("project manager error", "error", err)
		return 2
	}
	plans, err := plan.NewManager(cfg.StateDir)
	if err != nil {
		logger.Error("plan manager error", "error", err)
		return 2
	}

	processes := proc.NewManager(
		cfg.WorkspaceRoot,
		cfg.CommandPolicy,
		cfg.AllowedCommands,
		cfg.Shell,
		cfg.MaxConcurrentProcess,
		cfg.MaxProcessOutput,
		cfg.DefaultProcessTimeout,
	)

	server := mcp.NewServer(
		"CodeForge MCP",
		version,
		"Act as the coding agent. Begin with project_list and select or create a project. For non-trivial work, inspect plan_list and create or resume a structured plan with phases and tasks. Keep task status, blockers, notes, and verification evidence current as work proceeds. File, Git, and command tools operate on the active project. Use hashline file_read plus file_edit for precise edits. Use command_run for normal commands and process_start only for servers, watchers, interactive programs, or known long jobs. Finish only after plan tasks are reconciled, git_diff is reviewed, and relevant validation is recorded.",
		logger,
	)
	(&tools.Registry{Projects: projects, Plans: plans, Processes: processes, ForegroundYield: cfg.ForegroundYield}).Register(server)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("CodeForge MCP starting", "transport", "stdio", "workspace_root", cfg.WorkspaceRoot, "state_dir", cfg.StateDir, "active_project", projects.ActiveID(), "version", version)
	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		logger.Error("MCP stdio server failed", "error", err)
		return 1
	}
	return 0
}
