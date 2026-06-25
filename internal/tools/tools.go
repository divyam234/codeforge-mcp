package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/divyam234/codeforge-mcp/internal/mcp"
	"github.com/divyam234/codeforge-mcp/internal/plan"
	proc "github.com/divyam234/codeforge-mcp/internal/process"
	"github.com/divyam234/codeforge-mcp/internal/project"
	"github.com/divyam234/codeforge-mcp/internal/workspace"
)

type Registry struct {
	Projects        *project.Manager
	Plans           *plan.Manager
	Processes       *proc.Manager
	ForegroundYield time.Duration
}

type NoInput struct{}

type ProjectListInput struct {
	ActiveOnly bool `json:"active_only,omitempty" jsonschema:"Return only the active project summary instead of the full list."`
}

type ProjectListOutput struct {
	WorkspaceRoot string            `json:"workspace_root"`
	ActiveProject string            `json:"active_project,omitempty"`
	Projects      []project.Summary `json:"projects"`
	Count         int               `json:"count"`
}

type ProjectOutput struct {
	Project project.Summary `json:"project"`
}

type ProjectSelectInput struct {
	ID string `json:"id" jsonschema:"Project ID returned by project_list."`
}

type PlanListOutput struct {
	ProjectID    string         `json:"project_id"`
	ActivePlanID string         `json:"active_plan_id,omitempty"`
	Plans        []plan.Summary `json:"plans"`
	Count        int            `json:"count"`
}

type PlanGetInput struct {
	PlanID string `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	Select bool   `json:"select,omitempty" jsonschema:"Activate this plan before returning it."`
}

type PlanSelectInput struct {
	PlanID string `json:"plan_id" jsonschema:"Plan ID returned by plan_list."`
}

type WorkspaceInfoOutput struct {
	WorkspaceRoot    string           `json:"workspace_root"`
	ActiveProject    *project.Summary `json:"active_project,omitempty"`
	ProjectCount     int              `json:"project_count"`
	CommandPolicy    string           `json:"command_policy"`
	AllowedCommands  []string         `json:"allowed_commands,omitempty"`
	DeleteEnabled    bool             `json:"delete_enabled"`
	StateDir         string           `json:"state_dir,omitempty"`
	PlanningWorkflow string           `json:"planning_workflow"`
	EditingWorkflow  string           `json:"editing_workflow"`
	CommandWorkflow  string           `json:"command_workflow"`
}

type WorkspaceTreeInput struct {
	Path          string `json:"path,omitempty" jsonschema:"Directory relative to the active project."`
	Depth         int    `json:"depth,omitempty" jsonschema:"Maximum recursion depth from 0 to 20."`
	MaxEntries    int    `json:"max_entries,omitempty" jsonschema:"Maximum number of entries to return."`
	IncludeHidden bool   `json:"include_hidden,omitempty" jsonschema:"Include hidden files except ignored dependency and VCS directories."`
}

type WorkspaceTreeOutput struct {
	ProjectID string                `json:"project_id"`
	Entries   []workspace.TreeEntry `json:"entries"`
	Truncated bool                  `json:"truncated"`
}

type FileFindInput struct {
	Pattern       string `json:"pattern" jsonschema:"Glob pattern such as **/*.go."`
	Path          string `json:"path,omitempty" jsonschema:"Starting directory relative to the active project."`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type FileFindOutput struct {
	ProjectID string   `json:"project_id"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type FileReadInput struct {
	Path      string `json:"path" jsonschema:"File path relative to the active project."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"First line, one-based."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Last line, inclusive."`
	Mode      string `json:"mode,omitempty" jsonschema:"Use hashline before editing; plain is for display only."`
}

type CodeSearchInput struct {
	Query         string `json:"query"`
	Path          string `json:"path,omitempty"`
	Glob          string `json:"glob,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type CodeSearchOutput struct {
	ProjectID string                  `json:"project_id"`
	Matches   []workspace.SearchMatch `json:"matches"`
	Truncated bool                    `json:"truncated"`
}

type FileWriteInput struct {
	Path             string `json:"path"`
	Content          string `json:"content"`
	ExpectedSnapshot string `json:"expected_snapshot,omitempty"`
	CreateOnly       bool   `json:"create_only,omitempty"`
}

type FileEditInput struct {
	Path     string               `json:"path"`
	Snapshot string               `json:"snapshot"`
	Edits    []workspace.HashEdit `json:"edits"`
}

type FileMoveInput struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type FileMoveOutput struct {
	Moved bool   `json:"moved"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type FileDeleteInput struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

type FileDeleteOutput struct {
	Deleted bool   `json:"deleted"`
	Path    string `json:"path"`
}

type PatchApplyInput struct {
	Patch     string `json:"patch"`
	CheckOnly bool   `json:"check_only,omitempty"`
}

type PatchApplyOutput struct {
	Applied   bool   `json:"applied"`
	Valid     bool   `json:"valid"`
	CheckOnly bool   `json:"check_only"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
}

type GitStatusOutput struct {
	ProjectID string `json:"project_id"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
}

type GitDiffInput struct {
	Staged   bool   `json:"staged,omitempty"`
	Revision string `json:"revision,omitempty"`
	Path     string `json:"path,omitempty"`
	Stat     bool   `json:"stat,omitempty"`
}

type GitDiffOutput struct {
	ProjectID string `json:"project_id"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

type CommandInput struct {
	Command        string            `json:"command,omitempty" jsonschema:"Shell command. Prefer argv unless shell syntax is required."`
	Argv           []string          `json:"argv,omitempty" jsonschema:"Executable followed by arguments."`
	CWD            string            `json:"cwd,omitempty" jsonschema:"Working directory relative to the active project."`
	Environment    map[string]string `json:"environment,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type CommandRunInput struct {
	Command        string            `json:"command,omitempty" jsonschema:"Shell command. Prefer argv unless shell syntax is required."`
	Argv           []string          `json:"argv,omitempty" jsonschema:"Executable followed by arguments."`
	CWD            string            `json:"cwd,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	YieldTimeMS    int               `json:"yield_time_ms,omitempty" jsonschema:"How long to wait synchronously before promoting to a background session; maximum 30000."`
}

type ProcessPollInput struct {
	SessionID string `json:"session_id,omitempty"`
	Cursor    int64  `json:"cursor,omitempty"`
	ListAll   bool   `json:"list_all,omitempty" jsonschema:"Return all process sessions instead of polling a specific one."`
}

type ProcessPollOutput struct {
	SessionID string                `json:"session_id,omitempty"`
	Status    string                `json:"status,omitempty"`
	Output    string                `json:"output,omitempty"`
	Cursor    int64                 `json:"cursor,omitempty"`
	Truncated bool                  `json:"truncated,omitempty"`
	Processes []proc.ProcessSummary `json:"processes,omitempty"`
	Count     int                   `json:"count,omitempty"`
}

type ProcessListOutput struct {
	Processes []proc.ProcessSummary `json:"processes"`
	Count     int                   `json:"count"`
}

type ProcessWriteInput struct {
	SessionID  string `json:"session_id"`
	Input      string `json:"input,omitempty"`
	CloseAfter bool   `json:"close_after,omitempty"`
}

type ProcessWriteOutput struct {
	Written     int  `json:"written"`
	StdinClosed bool `json:"stdin_closed"`
}

type SessionInput struct {
	SessionID string `json:"session_id"`
}

type CancelOutput struct {
	Cancelled bool `json:"cancelled"`
}

type ForgetOutput struct {
	Forgotten bool `json:"forgotten"`
}

func (r *Registry) Register(server *mcp.Server) {
	mcp.AddTool(server, readTool("project_list", "List projects", "List projects under the workspace root and identify the active project. Use active_only to return only the active project summary."), r.projectList)
	mcp.AddTool(server, localWriteTool("project_create", "Create project", "Create a complete project directory from an empty, Go, Rust, Node, or Python template; initialize Git by default; and select it by default."), r.projectCreate)
	mcp.AddTool(server, localWriteTool("project_select", "Select project", "Select which project subsequent file, Git, and command tools operate on."), r.projectSelect)
	mcp.AddTool(server, readTool("plan_list", "List work plans", "List structured coding work plans for the active project and identify the active plan."), r.planList)
	mcp.AddTool(server, localWriteTool("plan_create", "Create work plan", "Create and optionally activate a persistent coding plan with ordered phases, tasks, dependencies, priorities, and acceptance criteria."), r.planCreate)
	mcp.AddTool(server, readTool("plan_get", "Inspect or select work plan", "Return a plan with progress, ready tasks, blocked tasks, notes, and verification evidence. Defaults to the active plan. Use select to activate a plan."), r.planGet)
	mcp.AddTool(server, localWriteTool("plan_update", "Update work plan", "Update plan metadata or lifecycle status. A plan cannot complete while phases or tasks remain open."), r.planUpdate)
	mcp.AddTool(server, localWriteTool("phase_update", "Update plan phase", "Update a phase status, summary, or blocker. Completing a phase requires all tasks to be terminal."), r.phaseUpdate)
	mcp.AddTool(server, localWriteTool("phase_add", "Add plan phase", "Append a new ordered phase with optional tasks when inspection reveals additional work."), r.phaseAdd)
	mcp.AddTool(server, localWriteTool("task_add", "Add plan task", "Add an actionable task to a plan phase with priority, acceptance criteria, and task-key dependencies."), r.taskAdd)
	mcp.AddTool(server, localWriteTool("task_update", "Update plan task", "Record task status, progress notes, blockers, and verification evidence; plan and phase progress update automatically."), r.taskUpdate)
	mcp.AddTool(server, readTool("workspace_info", "Workspace information", "Inspect the projects root, active project, task state directory, and recommended planning, edit, and command workflows."), r.workspaceInfo)
	mcp.AddTool(server, readTool("workspace_tree", "List project tree", "List files and directories under the active project with bounded recursion."), r.workspaceTree)
	mcp.AddTool(server, readTool("file_find", "Find files", "Find files in the active project using glob patterns such as **/*.go."), r.fileFind)
	mcp.AddTool(server, readTool("file_read", "Read file", "Read UTF-8 text with a file snapshot and per-line hashes. Use hashline mode before file_edit."), r.fileRead)
	mcp.AddTool(server, readTool("code_search", "Search code", "Search project text files with literal or regular-expression matching and return line hashes."), r.codeSearch)
	mcp.AddTool(server, localWriteTool("file_write", "Write file", "Atomically create or replace a UTF-8 file. Use expected_snapshot when replacing an existing file."), r.fileWrite)
	mcp.AddTool(server, localWriteTool("file_edit", "Hash-anchored line edit", "Atomically apply stale-safe line replacements and insertions against one file_read snapshot."), r.fileEdit)
	mcp.AddTool(server, localWriteTool("file_move", "Move file", "Move or rename a file or directory inside the active project."), r.fileMove)
	mcp.AddTool(server, destructiveTool("file_delete", "Delete path", "Delete a file or directory inside the active project."), r.fileDelete)
	mcp.AddTool(server, localWriteTool("patch_apply", "Apply unified diff", "Validate or apply a standard unified diff. Prefer file_edit for small precise changes."), r.patchApply)
	mcp.AddTool(server, readTool("git_status", "Git status", "Return porcelain Git status for the active project."), r.gitStatus)
	mcp.AddTool(server, readTool("git_diff", "Git diff", "Return a bounded no-color Git diff for the active project."), r.gitDiff)
	mcp.AddTool(server, unrestrictedCommandTool("command_run", "Run command", "Run an unrestricted command in the active project. Short commands complete inline; longer commands are promoted to a retained process session."), r.commandRun)
	mcp.AddTool(server, unrestrictedCommandTool("process_start", "Start long-running process", "Start an unrestricted server, watcher, interactive command, or known long build as a retained background session."), r.processStart)
	mcp.AddTool(server, readTool("process_poll", "Poll or list processes", "Read incremental output and completion state from a retained process session. Use list_all with no session_id to list all processes."), r.processPoll)
	mcp.AddTool(server, localWriteTool("process_write_stdin", "Write process stdin", "Write input to a retained running process and optionally close stdin."), r.processWrite)
	mcp.AddTool(server, destructiveTool("process_cancel", "Cancel process", "Terminate a retained process and its process group."), r.processCancel)
	mcp.AddTool(server, localWriteTool("process_forget", "Forget process", "Remove a completed process session and retained output from memory."), r.processForget)
}

func (r *Registry) projectList(_ context.Context, input ProjectListInput) (ProjectListOutput, error) {
	if input.ActiveOnly {
		_, summary, err := r.Projects.Active()
		if err != nil {
			return ProjectListOutput{}, err
		}
		return ProjectListOutput{WorkspaceRoot: r.Projects.Root(), ActiveProject: summary.ID, Projects: []project.Summary{summary}, Count: 1}, nil
	}
	projects, err := r.Projects.List()
	if err != nil {
		return ProjectListOutput{}, err
	}
	return ProjectListOutput{WorkspaceRoot: r.Projects.Root(), ActiveProject: r.Projects.ActiveID(), Projects: projects, Count: len(projects)}, nil
}

func (r *Registry) projectCreate(_ context.Context, input project.CreateRequest) (project.CreateResult, error) {
	return r.Projects.Create(input)
}

func (r *Registry) projectSelect(_ context.Context, input ProjectSelectInput) (ProjectOutput, error) {
	value, err := r.Projects.Select(input.ID)
	return ProjectOutput{Project: value}, err
}

func (r *Registry) planList(context.Context, NoInput) (PlanListOutput, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return PlanListOutput{}, err
	}
	plans, active, err := r.Plans.List(projectInfo.ID)
	if err != nil {
		return PlanListOutput{}, err
	}
	return PlanListOutput{ProjectID: projectInfo.ID, ActivePlanID: active, Plans: plans, Count: len(plans)}, nil
}

func (r *Registry) planCreate(_ context.Context, input plan.CreateRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.Create(projectInfo.ID, input)
}

func (r *Registry) planGet(_ context.Context, input PlanGetInput) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	if input.Select && input.PlanID != "" {
		if _, err := r.Plans.Select(projectInfo.ID, input.PlanID); err != nil {
			return plan.View{}, err
		}
	}
	return r.Plans.Get(projectInfo.ID, input.PlanID)
}

func (r *Registry) planUpdate(_ context.Context, input plan.UpdatePlanRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.UpdatePlan(projectInfo.ID, input)
}

func (r *Registry) phaseUpdate(_ context.Context, input plan.UpdatePhaseRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.UpdatePhase(projectInfo.ID, input)
}

func (r *Registry) phaseAdd(_ context.Context, input plan.AddPhaseRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.AddPhase(projectInfo.ID, input)
}

func (r *Registry) taskAdd(_ context.Context, input plan.AddTaskRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.AddTask(projectInfo.ID, input)
}

func (r *Registry) taskUpdate(_ context.Context, input plan.UpdateTaskRequest) (plan.View, error) {
	projectInfo, err := r.activeProjectSummary()
	if err != nil {
		return plan.View{}, err
	}
	return r.Plans.UpdateTask(projectInfo.ID, input)
}

func (r *Registry) workspaceInfo(context.Context, NoInput) (WorkspaceInfoOutput, error) {
	projects, err := r.Projects.List()
	if err != nil {
		return WorkspaceInfoOutput{}, err
	}
	value := WorkspaceInfoOutput{
		WorkspaceRoot: r.Projects.Root(), ProjectCount: len(projects), CommandPolicy: r.Processes.Policy(),
		PlanningWorkflow: "plan_list -> plan_create or plan_get -> task_update during work -> plan_get and plan_update after validation",
		EditingWorkflow:  "file_read(mode=hashline) -> file_edit(snapshot + line hashes)",
		CommandWorkflow:  "command_run for normal commands; process_start only for servers, watchers, interactive programs, or known long jobs",
	}
	if r.Plans != nil {
		value.StateDir = r.Plans.StateDir()
	}
	if r.Processes.Policy() == "allowlist" {
		value.AllowedCommands = r.Processes.AllowedCommands()
	}
	if ws, summary, activeErr := r.Projects.Active(); activeErr == nil {
		value.ActiveProject = &summary
		value.DeleteEnabled = ws.AllowDelete
	}
	return value, nil
}

func (r *Registry) workspaceTree(_ context.Context, input WorkspaceTreeInput) (WorkspaceTreeOutput, error) {
	ws, projectInfo, err := r.active()
	if err != nil {
		return WorkspaceTreeOutput{}, err
	}
	if input.Depth == 0 {
		input.Depth = 2
	}
	entries, truncated, err := ws.Tree(input.Path, input.Depth, input.MaxEntries, input.IncludeHidden)
	return WorkspaceTreeOutput{ProjectID: projectInfo.ID, Entries: entries, Truncated: truncated}, err
}

func (r *Registry) fileFind(_ context.Context, input FileFindInput) (FileFindOutput, error) {
	ws, projectInfo, err := r.active()
	if err != nil {
		return FileFindOutput{}, err
	}
	paths, truncated, err := ws.Find(input.Pattern, input.Path, input.IncludeHidden, input.MaxResults)
	return FileFindOutput{ProjectID: projectInfo.ID, Paths: paths, Truncated: truncated}, err
}

func (r *Registry) fileRead(_ context.Context, input FileReadInput) (workspace.ReadResult, error) {
	ws, _, err := r.active()
	if err != nil {
		return workspace.ReadResult{}, err
	}
	return ws.ReadFile(input.Path, input.StartLine, input.EndLine, input.Mode)
}

func (r *Registry) codeSearch(_ context.Context, input CodeSearchInput) (CodeSearchOutput, error) {
	ws, projectInfo, err := r.active()
	if err != nil {
		return CodeSearchOutput{}, err
	}
	matches, truncated, err := ws.Search(input.Query, input.Path, input.Glob, input.Regex, input.CaseSensitive, input.IncludeHidden, input.MaxResults)
	return CodeSearchOutput{ProjectID: projectInfo.ID, Matches: matches, Truncated: truncated}, err
}

func (r *Registry) fileWrite(_ context.Context, input FileWriteInput) (workspace.WriteResult, error) {
	ws, _, err := r.active()
	if err != nil {
		return workspace.WriteResult{}, err
	}
	return ws.WriteFile(input.Path, input.Content, input.ExpectedSnapshot, input.CreateOnly)
}

func (r *Registry) fileEdit(_ context.Context, input FileEditInput) (workspace.EditResult, error) {
	ws, _, err := r.active()
	if err != nil {
		return workspace.EditResult{}, err
	}
	return ws.EditFile(input.Path, input.Snapshot, input.Edits)
}

func (r *Registry) fileMove(_ context.Context, input FileMoveInput) (FileMoveOutput, error) {
	ws, _, err := r.active()
	if err != nil {
		return FileMoveOutput{}, err
	}
	if err := ws.Move(input.From, input.To, input.Overwrite); err != nil {
		return FileMoveOutput{}, err
	}
	return FileMoveOutput{Moved: true, From: filepath.ToSlash(input.From), To: filepath.ToSlash(input.To)}, nil
}

func (r *Registry) fileDelete(_ context.Context, input FileDeleteInput) (FileDeleteOutput, error) {
	ws, _, err := r.active()
	if err != nil {
		return FileDeleteOutput{}, err
	}
	if err := ws.Delete(input.Path, input.Recursive); err != nil {
		return FileDeleteOutput{}, err
	}
	return FileDeleteOutput{Deleted: true, Path: filepath.ToSlash(input.Path)}, nil
}

func (r *Registry) patchApply(_ context.Context, input PatchApplyInput) (PatchApplyOutput, error) {
	ws, _, err := r.active()
	if err != nil {
		return PatchApplyOutput{}, err
	}
	result, callErr := ws.ApplyPatch(input.Patch, input.CheckOnly)
	return PatchApplyOutput{Applied: callErr == nil && !input.CheckOnly, Valid: callErr == nil, CheckOnly: input.CheckOnly, Output: result.Output, ExitCode: result.ExitCode}, callErr
}

func (r *Registry) gitStatus(context.Context, NoInput) (GitStatusOutput, error) {
	ws, projectInfo, err := r.active()
	if err != nil {
		return GitStatusOutput{}, err
	}
	result, err := ws.Git("status", "--porcelain=v1", "--branch", "--untracked-files=all")
	return GitStatusOutput{ProjectID: projectInfo.ID, Output: truncate(result.Output, 2<<20), ExitCode: result.ExitCode}, err
}

func (r *Registry) gitDiff(_ context.Context, input GitDiffInput) (GitDiffOutput, error) {
	ws, projectInfo, err := r.active()
	if err != nil {
		return GitDiffOutput{}, err
	}
	if strings.HasPrefix(input.Revision, "-") || strings.ContainsRune(input.Revision, '\x00') {
		return GitDiffOutput{}, errors.New("invalid revision")
	}
	args := []string{"diff", "--no-ext-diff", "--no-color"}
	if input.Stat {
		args = append(args, "--stat")
	}
	if input.Staged {
		args = append(args, "--cached")
	}
	if input.Revision != "" {
		args = append(args, input.Revision)
	}
	if input.Path != "" {
		if _, err := ws.Resolve(input.Path, false); err != nil {
			return GitDiffOutput{}, err
		}
		args = append(args, "--", filepath.ToSlash(input.Path))
	}
	result, err := ws.Git(args...)
	output, truncated := truncateFlag(result.Output, 4<<20)
	return GitDiffOutput{ProjectID: projectInfo.ID, Output: output, ExitCode: result.ExitCode, Truncated: truncated}, err
}

func (r *Registry) commandRun(_ context.Context, input CommandRunInput) (proc.RunResult, error) {
	ws, _, err := r.active()
	if err != nil {
		return proc.RunResult{}, err
	}
	yield := r.ForegroundYield
	if input.YieldTimeMS > 0 {
		yield = time.Duration(input.YieldTimeMS) * time.Millisecond
	}
	return r.Processes.Run(proc.StartRequest{
		Command: input.Command, Argv: input.Argv, CWD: input.CWD, Environment: input.Environment,
		TimeoutSeconds: input.TimeoutSeconds, BaseDir: ws.Root,
	}, yield)
}

func (r *Registry) processStart(_ context.Context, input CommandInput) (proc.StartResult, error) {
	ws, _, err := r.active()
	if err != nil {
		return proc.StartResult{}, err
	}
	return r.Processes.Start(proc.StartRequest{
		Command: input.Command, Argv: input.Argv, CWD: input.CWD, Environment: input.Environment,
		TimeoutSeconds: input.TimeoutSeconds, BaseDir: ws.Root,
	})
}

func (r *Registry) processPoll(_ context.Context, input ProcessPollInput) (ProcessPollOutput, error) {
	if input.ListAll && input.SessionID == "" {
		processes := r.Processes.List()
		return ProcessPollOutput{Processes: processes, Count: len(processes)}, nil
	}
	poll, err := r.Processes.Poll(input.SessionID, input.Cursor)
	if err != nil {
		return ProcessPollOutput{}, err
	}
	return ProcessPollOutput{SessionID: poll.SessionID, Status: poll.Status, Output: poll.Output, Cursor: poll.NextCursor, Truncated: poll.Truncated}, nil
}

func (r *Registry) processWrite(_ context.Context, input ProcessWriteInput) (ProcessWriteOutput, error) {
	if err := r.Processes.WriteStdin(input.SessionID, input.Input, input.CloseAfter); err != nil {
		return ProcessWriteOutput{}, err
	}
	return ProcessWriteOutput{Written: len(input.Input), StdinClosed: input.CloseAfter}, nil
}

func (r *Registry) processCancel(_ context.Context, input SessionInput) (CancelOutput, error) {
	if err := r.Processes.Cancel(input.SessionID); err != nil {
		return CancelOutput{}, err
	}
	return CancelOutput{Cancelled: true}, nil
}

func (r *Registry) processForget(_ context.Context, input SessionInput) (ForgetOutput, error) {
	if err := r.Processes.Forget(input.SessionID); err != nil {
		return ForgetOutput{}, err
	}
	return ForgetOutput{Forgotten: true}, nil
}

func (r *Registry) activeProjectSummary() (project.Summary, error) {
	if r.Projects == nil {
		return project.Summary{}, errors.New("project manager is not configured")
	}
	if r.Plans == nil {
		return project.Summary{}, errors.New("plan manager is not configured")
	}
	_, summary, err := r.Projects.Active()
	return summary, err
}

func (r *Registry) active() (*workspace.Workspace, project.Summary, error) {
	if r.Projects == nil {
		return nil, project.Summary{}, errors.New("project manager is not configured")
	}
	return r.Projects.Active()
}

func readTool(name, title, description string) mcp.ToolSpec {
	return mcp.ToolSpec{
		Name:        name,
		Title:       title,
		Description: description,
		ReadOnly:    true,
		Idempotent:  true,
		OpenWorld:   false,
	}
}

func localWriteTool(name, title, description string) mcp.ToolSpec {
	return mcp.ToolSpec{
		Name:        name,
		Title:       title,
		Description: description,
		ReadOnly:    false,
		Destructive: false,
		Idempotent:  false,
		OpenWorld:   false,
	}
}

func destructiveTool(name, title, description string) mcp.ToolSpec {
	return mcp.ToolSpec{
		Name:        name,
		Title:       title,
		Description: description,
		ReadOnly:    false,
		Destructive: true,
		Idempotent:  false,
		OpenWorld:   false,
	}
}

func unrestrictedCommandTool(name, title, description string) mcp.ToolSpec {
	return mcp.ToolSpec{
		Name:        name,
		Title:       title,
		Description: description,
		ReadOnly:    false,
		Destructive: false,
		Idempotent:  false,
		OpenWorld:   true,
	}
}

func truncate(value string, max int) string {
	result, _ := truncateFlag(value, max)
	return result
}

func truncateFlag(value string, max int) (string, bool) {
	if len(value) <= max {
		return value, false
	}
	return value[:max] + fmt.Sprintf("\n… truncated %d bytes", len(value)-max), true
}
