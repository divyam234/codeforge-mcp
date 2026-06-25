package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func boolPtr(value bool) *bool { return &value }

func samplePlanRequest() CreateRequest {
	return CreateRequest{
		Title:     "Implement feature",
		Objective: "Ship a tested feature",
		Phases: []PhaseSeed{
			{
				Key: "inspect", Title: "Inspect", Objective: "Understand the code",
				Tasks: []TaskSeed{{Key: "audit", Title: "Audit repository", AcceptanceCriteria: []string{"Relevant files identified"}}},
			},
			{
				Key: "implement", Title: "Implement", Objective: "Make the change",
				Tasks: []TaskSeed{{Key: "code", Title: "Implement feature", DependsOn: []string{"audit"}}},
			},
			{
				Key: "validate", Title: "Validate", Objective: "Prove correctness",
				Tasks: []TaskSeed{{Key: "test", Title: "Run tests", DependsOn: []string{"code"}}},
			},
		},
	}
}

func TestCreatePersistsAndListsPlan(t *testing.T) {
	stateDir := t.TempDir()
	manager, err := NewManager(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if created.Plan.Status != StatusPlanned || len(created.ReadyTasks) != 1 || created.ReadyTasks[0].TaskKey != "audit" {
		t.Fatalf("unexpected created plan: %#v", created)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "plans.json")); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewManager(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	listed, active, err := reloaded.List("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || active != created.Plan.ID || !listed[0].Active {
		t.Fatalf("unexpected list after reload: %#v, active=%q", listed, active)
	}
}

func TestTaskUpdatesAdvancePhasesAndPlan(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	planID := created.Plan.ID

	view, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: planID, TaskID: "audit", Status: StatusInProgress, Note: "reading files"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Status != StatusInProgress || view.Plan.Phases[0].Status != StatusInProgress {
		t.Fatalf("plan did not enter progress: %#v", view.Plan)
	}

	view, err = manager.UpdateTask("demo", UpdateTaskRequest{PlanID: planID, TaskID: "audit", Status: StatusCompleted, Evidence: []string{"workspace_tree and code_search completed"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Phases[0].Status != StatusCompleted || view.Plan.ActivePhaseID != "phase-2" || len(view.ReadyTasks) != 1 || view.ReadyTasks[0].TaskKey != "code" {
		t.Fatalf("plan did not advance to implementation: %#v", view)
	}

	for _, key := range []string{"code", "test"} {
		if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: planID, TaskID: key, Status: StatusInProgress}); err != nil {
			t.Fatal(err)
		}
		view, err = manager.UpdateTask("demo", UpdateTaskRequest{PlanID: planID, TaskID: key, Status: StatusCompleted, Evidence: []string{"verified"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if view.Plan.Status != StatusCompleted || view.Progress.Percent != 100 || view.Plan.CompletedAt == nil {
		t.Fatalf("plan did not complete: %#v", view)
	}
}

func TestDependencyAndBlockerValidation(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "code", Status: StatusInProgress}); err == nil {
		t.Fatal("expected dependency error")
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "audit", Status: StatusBlocked}); err == nil {
		t.Fatal("expected missing blocker error")
	}
	view, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "audit", Status: StatusBlocked, Blocker: "missing specification"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Status != StatusBlocked || len(view.Blocked) != 1 {
		t.Fatalf("unexpected blocked view: %#v", view)
	}
}

func TestAddTaskAndSelectPlan(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("demo", CreateRequest{
		Title: "Second", Objective: "Second objective", Activate: boolPtr(false),
		Phases: []PhaseSeed{{Title: "Work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select("demo", second.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Plan.ID != second.Plan.ID {
		t.Fatalf("wrong selected plan: %#v", selected)
	}
	view, err := manager.AddTask("demo", AddTaskRequest{PhaseID: "work", Task: TaskSeed{Key: "new", Title: "New task", Priority: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Plan.Phases[0].Tasks) != 1 || view.Plan.Phases[0].Tasks[0].Key != "new" {
		t.Fatalf("task not added: %#v", view)
	}
	listed, active, err := manager.List("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || active != second.Plan.ID || first.Plan.ID == second.Plan.ID {
		t.Fatalf("unexpected plans: %#v active=%s", listed, active)
	}
}

func TestRejectsUnknownDependenciesAndIncompleteCompletion(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create("demo", CreateRequest{
		Title: "Bad", Objective: "Bad dependency",
		Phases: []PhaseSeed{{Title: "Work", Tasks: []TaskSeed{{Title: "Task", DependsOn: []string{"missing"}}}}},
	})
	if err == nil {
		t.Fatal("expected unknown dependency error")
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdatePlan("demo", UpdatePlanRequest{PlanID: created.Plan.ID, Status: StatusCompleted}); err == nil {
		t.Fatal("expected incomplete plan error")
	}
	if _, err := manager.UpdatePhase("demo", UpdatePhaseRequest{PlanID: created.Plan.ID, PhaseID: "inspect", Status: StatusCompleted}); err == nil {
		t.Fatal("expected incomplete phase error")
	}
}

func TestLaterPhaseCannotStartEarlyAndPhaseAddWorks(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "code", Status: StatusInProgress}); err == nil {
		t.Fatal("expected later phase ordering error")
	}
	view, err := manager.AddPhase("demo", AddPhaseRequest{
		PlanID: created.Plan.ID,
		Phase:  PhaseSeed{Key: "review", Title: "Review", Tasks: []TaskSeed{{Key: "diff", Title: "Review diff", DependsOn: []string{"test"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Plan.Phases) != 4 || view.Plan.Phases[3].Key != "review" || view.Plan.Phases[3].Tasks[0].ID == "" {
		t.Fatalf("phase not added correctly: %#v", view.Plan.Phases)
	}
}

func TestBlockedStateCannotBeClearedWithoutChangingStatus(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", samplePlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "audit", Status: StatusBlocked, Blocker: "waiting"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "audit", ClearBlocker: true}); err == nil {
		t.Fatal("expected blocked task validation error")
	}
	view, err := manager.Get("demo", created.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Plan.Phases[0].Tasks[0].Blocker != "waiting" || view.Plan.Phases[0].Tasks[0].Status != StatusBlocked {
		t.Fatalf("failed update mutated blocked task: %#v", view.Plan.Phases[0].Tasks[0])
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "audit", Status: StatusInProgress, ClearBlocker: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAddingTaskReopensCompletedPhaseAndPlan(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create("demo", CreateRequest{
		Title: "Small", Objective: "Complete then extend",
		Phases: []PhaseSeed{{Key: "work", Title: "Work", Tasks: []TaskSeed{{Key: "first", Title: "First"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "first", Status: StatusInProgress}); err != nil {
		t.Fatal(err)
	}
	completed, err := manager.UpdateTask("demo", UpdateTaskRequest{PlanID: created.Plan.ID, TaskID: "first", Status: StatusCompleted, Evidence: []string{"done"}})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Plan.Status != StatusCompleted {
		t.Fatalf("plan should be completed: %#v", completed)
	}
	reopened, err := manager.AddTask("demo", AddTaskRequest{PlanID: created.Plan.ID, PhaseID: "work", Task: TaskSeed{Key: "second", Title: "Second"}})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Plan.Status != StatusInProgress || reopened.Plan.Phases[0].Status != StatusPending || reopened.Progress.PendingTasks != 1 {
		t.Fatalf("plan did not reopen: %#v", reopened)
	}
}
