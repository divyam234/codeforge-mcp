package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/divyam234/codeforge-mcp/internal/mcp"
	"github.com/divyam234/codeforge-mcp/internal/plan"
	proc "github.com/divyam234/codeforge-mcp/internal/process"
	"github.com/divyam234/codeforge-mcp/internal/project"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func testRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	projects, err := project.NewManager(root, "", project.Limits{MaxFileBytes: 1 << 20, MaxSearchResults: 100, MaxTreeEntries: 100, AllowDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plan.NewManager(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	processes := proc.NewManager(root, "unrestricted", nil, "/bin/sh", 3, 1<<20, 5*time.Second)
	return &Registry{Projects: projects, Plans: plans, Processes: processes, ForegroundYield: 100 * time.Millisecond}, root
}

func connectRegistry(t *testing.T, registry *Registry) *sdkmcp.ClientSession {
	t.Helper()
	server := mcp.NewServer("test", "1", "instructions", slog.New(slog.NewTextHandler(io.Discard, nil)))
	registry.Register(server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.RunTransport(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tools-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-serverErrors:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return session
}

func call(t *testing.T, session *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool %s failed: %#v", name, result.Content)
	}
	return result
}

func TestRegisterExposesTypedProjectAndCommandTools(t *testing.T) {
	registry, _ := testRegistry(t)
	session := connectRegistry(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("tool %s lacks typed schemas", tool.Name)
		}
	}
	for _, name := range []string{"project_list", "project_create", "project_select", "plan_list", "plan_create", "plan_get", "phase_update", "phase_add", "task_add", "task_update", "file_read", "file_edit", "command_run", "process_start", "process_poll", "git_diff"} {
		if !names[name] {
			t.Fatalf("tool %s missing", name)
		}
	}
}

func TestToolAnnotationsMatchRuntimeCapabilities(t *testing.T) {
	registry, _ := testRegistry(t)
	session := connectRegistry(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	type expectedAnnotations struct {
		readOnly    bool
		destructive bool
		idempotent  bool
		openWorld   bool
	}

	expected := map[string]expectedAnnotations{}
	for _, name := range []string{
		"project_list", "plan_list", "plan_get", "workspace_info", "workspace_tree", "file_find",
		"file_read", "code_search", "git_status", "git_diff", "process_poll",
	} {
		expected[name] = expectedAnnotations{readOnly: true, idempotent: true}
	}
	for _, name := range []string{
		"project_create", "project_select", "plan_create", "plan_update", "phase_update", "phase_add", "task_add", "task_update",
		"file_write", "file_edit", "file_move", "patch_apply", "process_write_stdin", "process_forget",
	} {
		expected[name] = expectedAnnotations{}
	}
	for _, name := range []string{"file_delete", "process_cancel"} {
		expected[name] = expectedAnnotations{destructive: true}
	}
	for _, name := range []string{"command_run", "process_start"} {
		expected[name] = expectedAnnotations{openWorld: true}
	}

	if len(listed.Tools) != len(expected) {
		t.Fatalf("tool count = %d, want %d", len(listed.Tools), len(expected))
	}
	for _, tool := range listed.Tools {
		want, ok := expected[tool.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		if tool.Annotations == nil {
			t.Fatalf("tool %s has no annotations", tool.Name)
		}
		gotDestructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		gotOpenWorld := tool.Annotations.OpenWorldHint != nil && *tool.Annotations.OpenWorldHint
		if tool.Annotations.ReadOnlyHint != want.readOnly ||
			gotDestructive != want.destructive ||
			tool.Annotations.IdempotentHint != want.idempotent ||
			gotOpenWorld != want.openWorld {
			t.Fatalf(
				"tool %s annotations = {readOnly:%v destructive:%v idempotent:%v openWorld:%v}, want %+v",
				tool.Name,
				tool.Annotations.ReadOnlyHint,
				gotDestructive,
				tool.Annotations.IdempotentHint,
				gotOpenWorld,
				want,
			)
		}
	}
}

func TestProjectCreateFileEditAndForegroundCommandWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	registry, root := testRegistry(t)
	session := connectRegistry(t, registry)

	created := call(t, session, "project_create", map[string]any{"name": "Demo", "template": "go"})
	if created.StructuredContent == nil {
		t.Fatal("missing structured create output")
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "go.mod")); err != nil {
		t.Fatal(err)
	}

	readResult := call(t, session, "file_read", map[string]any{"path": "main.go", "mode": "hashline"})
	read := readResult.StructuredContent.(map[string]any)
	content := read["content"].(string)
	var lineNumber float64
	var hash string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "fmt.Println") {
			left, _, _ := strings.Cut(line, "|")
			parts := strings.SplitN(left, ":", 2)
			fmt.Sscanf(parts[0], "%f", &lineNumber)
			hash = parts[1]
		}
	}
	if hash == "" {
		t.Fatalf("print line missing: %s", content)
	}
	call(t, session, "file_edit", map[string]any{
		"path": "main.go", "snapshot": read["snapshot"],
		"edits": []any{map[string]any{"mode": "replace", "start_line": int(lineNumber), "start_hash": hash, "replacement": "\tfmt.Println(\"changed\")"}},
	})
	data, err := os.ReadFile(filepath.Join(root, "demo", "main.go"))
	if err != nil || !strings.Contains(string(data), "changed") {
		t.Fatalf("edit missing: %s, %v", data, err)
	}

	result := call(t, session, "command_run", map[string]any{"argv": []any{"go", "test", "./..."}, "yield_time_ms": 1000})
	command := result.StructuredContent.(map[string]any)
	if command["mode"] != "completed" || command["status"] != "succeeded" {
		t.Fatalf("unexpected command result: %#v", command)
	}
}

func TestCommandRunPromotesAndProcessLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	registry, _ := testRegistry(t)
	if _, err := registry.Projects.Create(project.CreateRequest{Name: "Demo", Template: "empty"}); err != nil {
		t.Fatal(err)
	}
	session := connectRegistry(t, registry)
	result := call(t, session, "command_run", map[string]any{"command": "printf start; sleep 0.2; printf end", "yield_time_ms": 10})
	value := result.StructuredContent.(map[string]any)
	if value["mode"] != "background" || value["session_id"] == "" {
		t.Fatalf("expected promotion: %#v", value)
	}
	id := value["session_id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		poll := call(t, session, "process_poll", map[string]any{"session_id": id, "cursor": 0}).StructuredContent.(map[string]any)
		if poll["status"] != "running" {
			if poll["status"] != "succeeded" || !strings.Contains(poll["output"].(string), "end") {
				t.Fatalf("unexpected final poll: %#v", poll)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background command did not finish")
}

func TestProjectSelectionAndWorkspaceTools(t *testing.T) {
	registry, root := testRegistry(t)
	falseValue := false
	for _, name := range []string{"one", "two"} {
		if _, err := registry.Projects.Create(project.CreateRequest{Name: name, Directory: name, Template: "empty", GitInit: &falseValue, Select: &falseValue}); err != nil {
			t.Fatal(err)
		}
	}
	session := connectRegistry(t, registry)
	list := call(t, session, "project_list", map[string]any{}).StructuredContent.(map[string]any)
	if list["count"].(float64) != 2 {
		t.Fatalf("unexpected project list: %#v", list)
	}
	call(t, session, "project_select", map[string]any{"id": "one"})
	call(t, session, "file_write", map[string]any{"path": "pkg/a.go", "content": "package pkg\n\nfunc Alpha() {}\n", "create_only": true})
	call(t, session, "workspace_tree", map[string]any{"path": ".", "depth": 3})
	found := call(t, session, "file_find", map[string]any{"pattern": "**/*.go"}).StructuredContent.(map[string]any)
	if len(found["paths"].([]any)) != 1 {
		t.Fatalf("unexpected find result: %#v", found)
	}
	searched := call(t, session, "code_search", map[string]any{"query": "Alpha", "glob": "**/*.go", "case_sensitive": true}).StructuredContent.(map[string]any)
	if len(searched["matches"].([]any)) != 1 {
		t.Fatalf("unexpected search result: %#v", searched)
	}
	call(t, session, "file_move", map[string]any{"from": "pkg/a.go", "to": "pkg/b.go"})
	call(t, session, "file_delete", map[string]any{"path": "pkg/b.go"})
	if _, err := os.Stat(filepath.Join(root, "one", "pkg", "b.go")); !os.IsNotExist(err) {
		t.Fatalf("file was not deleted: %v", err)
	}
}

func TestPatchGitAndRetainedProcessTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process commands")
	}
	registry, _ := testRegistry(t)
	if _, err := registry.Projects.Create(project.CreateRequest{Name: "repo", Template: "empty"}); err != nil {
		t.Fatal(err)
	}
	session := connectRegistry(t, registry)
	call(t, session, "file_write", map[string]any{"path": "a.txt", "content": "old\n", "create_only": true})
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"
	checked := call(t, session, "patch_apply", map[string]any{"patch": patch, "check_only": true}).StructuredContent.(map[string]any)
	if checked["valid"] != true || checked["applied"] != false {
		t.Fatalf("unexpected check result: %#v", checked)
	}
	call(t, session, "patch_apply", map[string]any{"patch": patch})
	call(t, session, "git_status", map[string]any{})
	call(t, session, "git_diff", map[string]any{"path": "a.txt"})

	started := call(t, session, "process_start", map[string]any{"command": "cat"}).StructuredContent.(map[string]any)
	id := started["session_id"].(string)
	call(t, session, "process_write_stdin", map[string]any{"session_id": id, "input": "hello\n", "close_after": true})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		poll := call(t, session, "process_poll", map[string]any{"session_id": id}).StructuredContent.(map[string]any)
		if poll["status"] != "running" {
			if poll["status"] != "succeeded" || poll["output"] != "hello\n" {
				t.Fatalf("unexpected process result: %#v", poll)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	listed := call(t, session, "process_poll", map[string]any{"list_all": true}).StructuredContent.(map[string]any)
	if listed["count"].(float64) < 1 {
		t.Fatalf("process list empty: %#v", listed)
	}
	call(t, session, "process_forget", map[string]any{"session_id": id})

	cancelled := call(t, session, "process_start", map[string]any{"command": "sleep 5"}).StructuredContent.(map[string]any)
	cancelID := cancelled["session_id"].(string)
	call(t, session, "process_cancel", map[string]any{"session_id": cancelID})
}

func TestCommandFailureIsStructuredResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process commands")
	}
	registry, _ := testRegistry(t)
	falseValue := false
	if _, err := registry.Projects.Create(project.CreateRequest{Name: "demo", Template: "empty", GitInit: &falseValue}); err != nil {
		t.Fatal(err)
	}
	session := connectRegistry(t, registry)
	value := call(t, session, "command_run", map[string]any{"command": "printf failure; exit 7", "yield_time_ms": 1000}).StructuredContent.(map[string]any)
	if value["mode"] != "completed" || value["status"] != "failed" || value["exit_code"].(float64) != 7 {
		t.Fatalf("unexpected failed command result: %#v", value)
	}
}

func TestPlanAndTaskTrackingWorkflow(t *testing.T) {
	registry, _ := testRegistry(t)
	falseValue := false
	if _, err := registry.Projects.Create(project.CreateRequest{Name: "demo", Template: "empty", GitInit: &falseValue}); err != nil {
		t.Fatal(err)
	}
	session := connectRegistry(t, registry)

	created := call(t, session, "plan_create", map[string]any{
		"title":     "Implement task tracking",
		"objective": "Ship tested task tracking",
		"phases": []any{
			map[string]any{"key": "inspect", "title": "Inspect", "tasks": []any{map[string]any{"key": "audit", "title": "Audit code"}}},
			map[string]any{"key": "implement", "title": "Implement", "tasks": []any{map[string]any{"key": "code", "title": "Write code", "depends_on": []any{"audit"}}}},
			map[string]any{"key": "validate", "title": "Validate", "tasks": []any{map[string]any{"key": "test", "title": "Run tests", "depends_on": []any{"code"}}}},
		},
	}).StructuredContent.(map[string]any)
	planValue := created["plan"].(map[string]any)
	if planValue["status"] != "planned" || len(created["ready_tasks"].([]any)) != 1 {
		t.Fatalf("unexpected created plan: %#v", created)
	}

	call(t, session, "task_update", map[string]any{"task_id": "audit", "status": "in_progress", "note": "reading source"})
	updated := call(t, session, "task_update", map[string]any{"task_id": "audit", "status": "completed", "evidence": []any{"workspace inspected"}}).StructuredContent.(map[string]any)
	if updated["plan"].(map[string]any)["active_phase_id"] != "phase-2" {
		t.Fatalf("plan did not advance: %#v", updated)
	}

	listed := call(t, session, "plan_list", map[string]any{}).StructuredContent.(map[string]any)
	if listed["count"].(float64) != 1 || listed["active_plan_id"] == "" {
		t.Fatalf("unexpected plan list: %#v", listed)
	}
	got := call(t, session, "plan_get", map[string]any{}).StructuredContent.(map[string]any)
	if got["progress"].(map[string]any)["completed_tasks"].(float64) != 1 {
		t.Fatalf("progress not recorded: %#v", got)
	}
}

func TestFileToolRequiresActiveProject(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	projects, err := project.NewManager(root, "", project.Limits{MaxFileBytes: 1 << 20, MaxSearchResults: 100, MaxTreeEntries: 100, AllowDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plan.NewManager(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	registry := &Registry{Projects: projects, Plans: plans, Processes: proc.NewManager(root, "unrestricted", nil, "/bin/sh", 2, 1<<20, time.Second), ForegroundYield: time.Second}
	session := connectRegistry(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "file_read", Arguments: map[string]any{"path": "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected no-active-project tool error: %#v", result)
	}
}
