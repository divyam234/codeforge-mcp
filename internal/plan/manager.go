package plan

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const stateVersion = 1

type Status string

const (
	StatusPlanned    Status = "planned"
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusCompleted  Status = "completed"
	StatusSkipped    Status = "skipped"
	StatusCancelled  Status = "cancelled"
)

type TaskSeed struct {
	Key                string   `json:"key,omitempty" jsonschema:"Optional stable key used by dependencies and later updates."`
	Title              string   `json:"title" jsonschema:"Short actionable task title."`
	Description        string   `json:"description,omitempty" jsonschema:"Implementation details or scope."`
	Priority           string   `json:"priority,omitempty" jsonschema:"Priority: low, medium, high, or critical. Defaults to medium."`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty" jsonschema:"Observable conditions that prove the task is complete."`
	DependsOn          []string `json:"depends_on,omitempty" jsonschema:"Task keys this task depends on."`
}

type PhaseSeed struct {
	Key       string     `json:"key,omitempty" jsonschema:"Optional stable phase key."`
	Title     string     `json:"title" jsonschema:"Phase title such as Inspect, Implement, or Validate."`
	Objective string     `json:"objective,omitempty" jsonschema:"Outcome expected from this phase."`
	Tasks     []TaskSeed `json:"tasks,omitempty"`
}

type CreateRequest struct {
	Title       string      `json:"title" jsonschema:"Short name for the coding work plan."`
	Objective   string      `json:"objective" jsonschema:"Concrete user-visible outcome of the work."`
	Phases      []PhaseSeed `json:"phases" jsonschema:"Ordered execution phases. At least one phase is required."`
	Activate    *bool       `json:"activate,omitempty" jsonschema:"Make this the active plan. Defaults to true."`
	ReplaceOpen bool        `json:"replace_open,omitempty" jsonschema:"Cancel the current open plan before creating this one."`
}

type Task struct {
	ID                 string    `json:"id"`
	Key                string    `json:"key"`
	Title              string    `json:"title"`
	Description        string    `json:"description,omitempty"`
	Status             Status    `json:"status"`
	Priority           string    `json:"priority"`
	AcceptanceCriteria []string  `json:"acceptance_criteria,omitempty"`
	DependsOn          []string  `json:"depends_on,omitempty"`
	Notes              []string  `json:"notes,omitempty"`
	Evidence           []string  `json:"evidence,omitempty"`
	Blocker            string    `json:"blocker,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Phase struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Objective string    `json:"objective,omitempty"`
	Status    Status    `json:"status"`
	Summary   string    `json:"summary,omitempty"`
	Blocker   string    `json:"blocker,omitempty"`
	Tasks     []Task    `json:"tasks"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Plan struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"project_id"`
	Title         string     `json:"title"`
	Objective     string     `json:"objective"`
	Status        Status     `json:"status"`
	Summary       string     `json:"summary,omitempty"`
	ActivePhaseID string     `json:"active_phase_id,omitempty"`
	Phases        []Phase    `json:"phases"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type Progress struct {
	TotalTasks     int `json:"total_tasks"`
	PendingTasks   int `json:"pending_tasks"`
	InProgress     int `json:"in_progress_tasks"`
	BlockedTasks   int `json:"blocked_tasks"`
	CompletedTasks int `json:"completed_tasks"`
	SkippedTasks   int `json:"skipped_tasks"`
	CancelledTasks int `json:"cancelled_tasks"`
	Percent        int `json:"percent"`
}

type TaskRef struct {
	PhaseID    string `json:"phase_id"`
	PhaseTitle string `json:"phase_title"`
	TaskID     string `json:"task_id"`
	TaskKey    string `json:"task_key"`
	Title      string `json:"title"`
	Priority   string `json:"priority"`
}

type View struct {
	Plan       Plan      `json:"plan"`
	Progress   Progress  `json:"progress"`
	ReadyTasks []TaskRef `json:"ready_tasks,omitempty"`
	Blocked    []TaskRef `json:"blocked_tasks,omitempty"`
}

type Summary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Objective    string    `json:"objective"`
	Status       Status    `json:"status"`
	Active       bool      `json:"active"`
	Progress     Progress  `json:"progress"`
	UpdatedAt    time.Time `json:"updated_at"`
	CurrentPhase string    `json:"current_phase,omitempty"`
}

type UpdatePlanRequest struct {
	PlanID    string `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	Title     string `json:"title,omitempty"`
	Objective string `json:"objective,omitempty"`
	Status    Status `json:"status,omitempty" jsonschema:"planned, in_progress, blocked, completed, or cancelled."`
	Summary   string `json:"summary,omitempty" jsonschema:"Concise implementation or completion summary."`
}

type UpdatePhaseRequest struct {
	PlanID       string `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	PhaseID      string `json:"phase_id" jsonschema:"Phase ID or key."`
	Status       Status `json:"status,omitempty" jsonschema:"pending, in_progress, blocked, completed, skipped, or cancelled."`
	Summary      string `json:"summary,omitempty"`
	Blocker      string `json:"blocker,omitempty" jsonschema:"Reason this phase cannot proceed. Required when setting blocked."`
	ClearBlocker bool   `json:"clear_blocker,omitempty"`
}

type AddPhaseRequest struct {
	PlanID string    `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	Phase  PhaseSeed `json:"phase"`
}

type AddTaskRequest struct {
	PlanID  string   `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	PhaseID string   `json:"phase_id" jsonschema:"Phase ID or key."`
	Task    TaskSeed `json:"task"`
}

type UpdateTaskRequest struct {
	PlanID       string   `json:"plan_id,omitempty" jsonschema:"Plan ID. Defaults to the active plan."`
	TaskID       string   `json:"task_id" jsonschema:"Task ID or key."`
	Status       Status   `json:"status,omitempty" jsonschema:"pending, in_progress, blocked, completed, skipped, or cancelled."`
	Note         string   `json:"note,omitempty" jsonschema:"Short progress note appended to the task."`
	Evidence     []string `json:"evidence,omitempty" jsonschema:"Commands, test results, files, or observations proving progress/completion."`
	Blocker      string   `json:"blocker,omitempty" jsonschema:"Reason work cannot proceed. Required when setting blocked."`
	ClearBlocker bool     `json:"clear_blocker,omitempty"`
}

type projectState struct {
	ActivePlanID string           `json:"active_plan_id,omitempty"`
	Plans        map[string]*Plan `json:"plans"`
}

type persistedState struct {
	Version  int                      `json:"version"`
	Projects map[string]*projectState `json:"projects"`
}

type Manager struct {
	mu       sync.RWMutex
	stateDir string
	state    persistedState
	now      func() time.Time
}

func NewManager(stateDir string) (*Manager, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("plan state directory is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve plan state directory: %w", err)
	}
	m := &Manager{
		stateDir: abs,
		state:    persistedState{Version: stateVersion, Projects: map[string]*projectState{}},
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) StateDir() string { return m.stateDir }

func (m *Manager) List(projectID string) ([]Summary, string, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return nil, "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ps := m.state.Projects[projectID]
	if ps == nil {
		return []Summary{}, "", nil
	}
	result := make([]Summary, 0, len(ps.Plans))
	for _, p := range ps.Plans {
		currentPhase := ""
		for _, phase := range p.Phases {
			if phase.ID == p.ActivePhaseID {
				currentPhase = phase.Title
				break
			}
		}
		result = append(result, Summary{
			ID: p.ID, Title: p.Title, Objective: p.Objective, Status: p.Status,
			Active: p.ID == ps.ActivePlanID, Progress: progress(*p), UpdatedAt: p.UpdatedAt,
			CurrentPhase: currentPhase,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, ps.ActivePlanID, nil
}

func (m *Manager) Create(projectID string, req CreateRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Objective = strings.TrimSpace(req.Objective)
	if req.Title == "" || req.Objective == "" {
		return View{}, errors.New("plan title and objective are required")
	}
	if len(req.Phases) == 0 {
		return View{}, errors.New("at least one phase is required")
	}
	now := m.now()
	p := &Plan{
		ID: newID("plan"), ProjectID: projectID, Title: req.Title, Objective: req.Objective,
		Status: StatusPlanned, CreatedAt: now, UpdatedAt: now,
	}
	phaseKeys := map[string]struct{}{}
	taskKeys := map[string]struct{}{}
	for phaseIndex, seed := range req.Phases {
		seed.Title = strings.TrimSpace(seed.Title)
		if seed.Title == "" {
			return View{}, fmt.Errorf("phase %d title is required", phaseIndex+1)
		}
		phaseKey := uniqueKey(seed.Key, seed.Title, phaseKeys)
		phase := Phase{
			ID: fmt.Sprintf("phase-%d", phaseIndex+1), Key: phaseKey, Title: seed.Title,
			Objective: strings.TrimSpace(seed.Objective), Status: StatusPending, CreatedAt: now, UpdatedAt: now,
		}
		for taskIndex, taskSeed := range seed.Tasks {
			task, err := taskFromSeed(taskSeed, fmt.Sprintf("task-%d-%d", phaseIndex+1, taskIndex+1), taskKeys, now)
			if err != nil {
				return View{}, fmt.Errorf("phase %q task %d: %w", seed.Title, taskIndex+1, err)
			}
			phase.Tasks = append(phase.Tasks, task)
		}
		p.Phases = append(p.Phases, phase)
	}
	if err := validateDependencies(p); err != nil {
		return View{}, err
	}
	if len(p.Phases) > 0 {
		p.ActivePhaseID = p.Phases[0].ID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	ps := m.ensureProject(projectID)
	if req.ReplaceOpen && ps.ActivePlanID != "" {
		if old := ps.Plans[ps.ActivePlanID]; old != nil && !terminalPlan(old.Status) {
			old.Status = StatusCancelled
			old.UpdatedAt = now
		}
	}
	ps.Plans[p.ID] = p
	activate := req.Activate == nil || *req.Activate
	if activate {
		ps.ActivePlanID = p.ID
	}
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) Get(projectID, planID string) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, err := m.lookupPlanLocked(projectID, planID)
	if err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) Select(projectID, planID string) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return View{}, errors.New("plan_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ps := m.state.Projects[projectID]
	if ps == nil || ps.Plans[planID] == nil {
		return View{}, fmt.Errorf("plan %q not found for project %q", planID, projectID)
	}
	ps.ActivePlanID = planID
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*ps.Plans[planID]), nil
}

func (m *Manager) UpdatePlan(projectID string, req UpdatePlanRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.lookupPlanLocked(projectID, req.PlanID)
	if err != nil {
		return View{}, err
	}
	if req.Status != "" {
		if !validPlanStatus(req.Status) {
			return View{}, fmt.Errorf("invalid plan status %q", req.Status)
		}
		if req.Status == StatusCompleted && !allPhasesTerminal(p) {
			return View{}, errors.New("cannot complete plan while phases or tasks remain open")
		}
	}
	if value := strings.TrimSpace(req.Title); value != "" {
		p.Title = value
	}
	if value := strings.TrimSpace(req.Objective); value != "" {
		p.Objective = value
	}
	if req.Summary != "" {
		p.Summary = strings.TrimSpace(req.Summary)
	}
	if req.Status != "" {
		p.Status = req.Status
		if req.Status == StatusCompleted {
			now := m.now()
			p.CompletedAt = &now
		} else {
			p.CompletedAt = nil
		}
	}
	p.UpdatedAt = m.now()
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) UpdatePhase(projectID string, req UpdatePhaseRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.lookupPlanLocked(projectID, req.PlanID)
	if err != nil {
		return View{}, err
	}
	phase, err := findPhase(p, req.PhaseID)
	if err != nil {
		return View{}, err
	}
	effectiveStatus := phase.Status
	if req.Status != "" {
		if !validWorkStatus(req.Status) {
			return View{}, fmt.Errorf("invalid phase status %q", req.Status)
		}
		if req.Status == StatusCompleted && !allTasksTerminal(phase) {
			return View{}, errors.New("cannot complete phase while tasks remain open")
		}
		effectiveStatus = req.Status
	}
	effectiveBlocker := phase.Blocker
	if req.ClearBlocker {
		effectiveBlocker = ""
	}
	if blocker := strings.TrimSpace(req.Blocker); blocker != "" {
		effectiveBlocker = blocker
	}
	if effectiveStatus == StatusBlocked && effectiveBlocker == "" {
		return View{}, errors.New("blocked phase requires a blocker")
	}
	if effectiveStatus != StatusBlocked && req.Status != "" {
		effectiveBlocker = ""
	}
	phase.Status = effectiveStatus
	phase.Blocker = effectiveBlocker
	if req.Status == StatusSkipped || req.Status == StatusCancelled {
		for i := range phase.Tasks {
			if !terminalWork(phase.Tasks[i].Status) {
				phase.Tasks[i].Status = req.Status
				phase.Tasks[i].UpdatedAt = m.now()
			}
		}
	}
	if req.Summary != "" {
		phase.Summary = strings.TrimSpace(req.Summary)
	}
	phase.UpdatedAt = m.now()
	recompute(p, m.now())
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) AddPhase(projectID string, req AddPhaseRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.lookupPlanLocked(projectID, req.PlanID)
	if err != nil {
		return View{}, err
	}
	seed := req.Phase
	seed.Title = strings.TrimSpace(seed.Title)
	if seed.Title == "" {
		return View{}, errors.New("phase title is required")
	}
	phaseKeys := map[string]struct{}{}
	taskKeys := map[string]struct{}{}
	for _, existingPhase := range p.Phases {
		phaseKeys[existingPhase.Key] = struct{}{}
		for _, existingTask := range existingPhase.Tasks {
			taskKeys[existingTask.Key] = struct{}{}
		}
	}
	now := m.now()
	phase := Phase{
		ID: fmt.Sprintf("phase-%d", len(p.Phases)+1), Key: uniqueKey(seed.Key, seed.Title, phaseKeys),
		Title: seed.Title, Objective: strings.TrimSpace(seed.Objective), Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	taskNumber := taskCount(p)
	for i, taskSeed := range seed.Tasks {
		taskNumber++
		task, err := taskFromSeed(taskSeed, fmt.Sprintf("task-%d", taskNumber), taskKeys, now)
		if err != nil {
			return View{}, fmt.Errorf("task %d: %w", i+1, err)
		}
		phase.Tasks = append(phase.Tasks, task)
	}
	p.Phases = append(p.Phases, phase)
	if err := validateDependencies(p); err != nil {
		p.Phases = p.Phases[:len(p.Phases)-1]
		return View{}, err
	}
	recompute(p, now)
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) AddTask(projectID string, req AddTaskRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.lookupPlanLocked(projectID, req.PlanID)
	if err != nil {
		return View{}, err
	}
	phase, err := findPhase(p, req.PhaseID)
	if err != nil {
		return View{}, err
	}
	keys := map[string]struct{}{}
	for _, phaseValue := range p.Phases {
		for _, taskValue := range phaseValue.Tasks {
			keys[taskValue.Key] = struct{}{}
		}
	}
	now := m.now()
	task, err := taskFromSeed(req.Task, nextTaskID(p), keys, now)
	if err != nil {
		return View{}, err
	}
	for _, dependency := range task.DependsOn {
		if _, ok := keys[dependency]; !ok {
			return View{}, fmt.Errorf("unknown dependency task key %q", dependency)
		}
	}
	phase.Tasks = append(phase.Tasks, task)
	phase.UpdatedAt = now
	recompute(p, now)
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) UpdateTask(projectID string, req UpdateTaskRequest) (View, error) {
	projectID, err := normalizeProjectID(projectID)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.lookupPlanLocked(projectID, req.PlanID)
	if err != nil {
		return View{}, err
	}
	phase, task, err := findTask(p, req.TaskID)
	if err != nil {
		return View{}, err
	}
	effectiveStatus := task.Status
	if req.Status != "" {
		if !validWorkStatus(req.Status) {
			return View{}, fmt.Errorf("invalid task status %q", req.Status)
		}
		if req.Status == StatusInProgress {
			if !phaseIsReady(p, phase) {
				return View{}, errors.New("cannot start task before earlier phases are completed, skipped, or cancelled")
			}
			if !dependenciesComplete(p, task) {
				return View{}, errors.New("cannot start task before its dependencies are completed or skipped")
			}
		}
		effectiveStatus = req.Status
	}
	effectiveBlocker := task.Blocker
	if req.ClearBlocker {
		effectiveBlocker = ""
	}
	if blocker := strings.TrimSpace(req.Blocker); blocker != "" {
		effectiveBlocker = blocker
	}
	if effectiveStatus == StatusBlocked && effectiveBlocker == "" {
		return View{}, errors.New("blocked task requires a blocker")
	}
	if effectiveStatus != StatusBlocked && req.Status != "" {
		effectiveBlocker = ""
	}
	task.Status = effectiveStatus
	task.Blocker = effectiveBlocker
	if note := strings.TrimSpace(req.Note); note != "" {
		task.Notes = appendLimited(task.Notes, note, 50)
	}
	for _, evidence := range req.Evidence {
		if value := strings.TrimSpace(evidence); value != "" {
			task.Evidence = appendLimited(task.Evidence, value, 50)
		}
	}
	now := m.now()
	task.UpdatedAt = now
	phase.UpdatedAt = now
	recompute(p, now)
	if err := m.saveLocked(); err != nil {
		return View{}, err
	}
	return view(*p), nil
}

func (m *Manager) ensureProject(projectID string) *projectState {
	ps := m.state.Projects[projectID]
	if ps == nil {
		ps = &projectState{Plans: map[string]*Plan{}}
		m.state.Projects[projectID] = ps
	}
	if ps.Plans == nil {
		ps.Plans = map[string]*Plan{}
	}
	return ps
}

func (m *Manager) lookupPlanLocked(projectID, planID string) (*Plan, error) {
	ps := m.state.Projects[projectID]
	if ps == nil {
		return nil, fmt.Errorf("no plans exist for project %q; call plan_create", projectID)
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		planID = ps.ActivePlanID
	}
	if planID == "" {
		return nil, fmt.Errorf("no active plan for project %q; call plan_list then plan_get(select=true), or plan_create", projectID)
	}
	p := ps.Plans[planID]
	if p == nil {
		return nil, fmt.Errorf("plan %q not found for project %q", planID, projectID)
	}
	return p, nil
}

func (m *Manager) load() error {
	path := filepath.Join(m.stateDir, "plans.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read plan state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode plan state: %w", err)
	}
	if state.Version != stateVersion {
		return fmt.Errorf("unsupported plan state version %d", state.Version)
	}
	if state.Projects == nil {
		state.Projects = map[string]*projectState{}
	}
	m.state = state
	return nil
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return fmt.Errorf("create plan state directory: %w", err)
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan state: %w", err)
	}
	path := filepath.Join(m.stateDir, "plans.json")
	tmp, err := os.CreateTemp(m.stateDir, ".plans-*.tmp")
	if err != nil {
		return fmt.Errorf("create plan state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write plan state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync plan state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close plan state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace plan state: %w", err)
	}
	return nil
}

func view(p Plan) View {
	ready, blocked := readyTasks(p)
	return View{Plan: p, Progress: progress(p), ReadyTasks: ready, Blocked: blocked}
}

func progress(p Plan) Progress {
	var result Progress
	for _, phase := range p.Phases {
		for _, task := range phase.Tasks {
			result.TotalTasks++
			switch task.Status {
			case StatusPending:
				result.PendingTasks++
			case StatusInProgress:
				result.InProgress++
			case StatusBlocked:
				result.BlockedTasks++
			case StatusCompleted:
				result.CompletedTasks++
			case StatusSkipped:
				result.SkippedTasks++
			case StatusCancelled:
				result.CancelledTasks++
			}
		}
	}
	if result.TotalTasks > 0 {
		result.Percent = (result.CompletedTasks + result.SkippedTasks) * 100 / result.TotalTasks
	}
	return result
}

func readyTasks(p Plan) ([]TaskRef, []TaskRef) {
	var ready, blocked []TaskRef
	for _, phase := range p.Phases {
		if terminalWork(phase.Status) {
			continue
		}
		for i := range phase.Tasks {
			task := &phase.Tasks[i]
			ref := TaskRef{PhaseID: phase.ID, PhaseTitle: phase.Title, TaskID: task.ID, TaskKey: task.Key, Title: task.Title, Priority: task.Priority}
			if task.Status == StatusBlocked {
				blocked = append(blocked, ref)
			} else if phase.ID == p.ActivePhaseID && task.Status == StatusPending && dependenciesComplete(&p, task) {
				ready = append(ready, ref)
			} else if task.Status == StatusInProgress {
				ready = append([]TaskRef{ref}, ready...)
			}
		}
	}
	return ready, blocked
}

func recompute(p *Plan, now time.Time) {
	blocked := false
	open := false
	allTerminal := true
	p.ActivePhaseID = ""
	for i := range p.Phases {
		phase := &p.Phases[i]
		phaseBlocked := false
		phaseInProgress := false
		phaseOpen := false
		phaseAllTerminal := true
		for _, task := range phase.Tasks {
			switch task.Status {
			case StatusBlocked:
				phaseBlocked = true
				phaseAllTerminal = false
			case StatusInProgress:
				phaseInProgress = true
				phaseOpen = true
				phaseAllTerminal = false
			case StatusPending:
				phaseOpen = true
				phaseAllTerminal = false
			}
		}
		switch {
		case phase.Blocker != "" || phaseBlocked:
			phase.Status = StatusBlocked
			blocked = true
			allTerminal = false
		case phaseAllTerminal:
			if phase.Status != StatusSkipped && phase.Status != StatusCancelled {
				phase.Status = StatusCompleted
			}
		case phaseInProgress:
			phase.Status = StatusInProgress
			open = true
			allTerminal = false
		case phaseOpen:
			phase.Status = StatusPending
			open = true
			allTerminal = false
		}
		if p.ActivePhaseID == "" && !terminalWork(phase.Status) {
			p.ActivePhaseID = phase.ID
		}
	}
	if p.Status != StatusCancelled {
		switch {
		case blocked:
			p.Status = StatusBlocked
			p.CompletedAt = nil
		case allTerminal:
			p.Status = StatusCompleted
			if p.CompletedAt == nil {
				completed := now
				p.CompletedAt = &completed
			}
		case open:
			p.Status = StatusInProgress
			p.CompletedAt = nil
		default:
			p.Status = StatusPlanned
			p.CompletedAt = nil
		}
	}
	p.UpdatedAt = now
}

func taskFromSeed(seed TaskSeed, id string, keys map[string]struct{}, now time.Time) (Task, error) {
	seed.Title = strings.TrimSpace(seed.Title)
	if seed.Title == "" {
		return Task{}, errors.New("task title is required")
	}
	priority := strings.ToLower(strings.TrimSpace(seed.Priority))
	if priority == "" {
		priority = "medium"
	}
	if priority != "low" && priority != "medium" && priority != "high" && priority != "critical" {
		return Task{}, fmt.Errorf("invalid priority %q", seed.Priority)
	}
	key := uniqueKey(seed.Key, seed.Title, keys)
	return Task{
		ID: id, Key: key, Title: seed.Title, Description: strings.TrimSpace(seed.Description),
		Status: StatusPending, Priority: priority, AcceptanceCriteria: cleanStrings(seed.AcceptanceCriteria, 20),
		DependsOn: cleanStrings(seed.DependsOn, 20), CreatedAt: now, UpdatedAt: now,
	}, nil
}

func validateDependencies(p *Plan) error {
	keys := map[string]struct{}{}
	for _, phase := range p.Phases {
		for _, task := range phase.Tasks {
			keys[task.Key] = struct{}{}
		}
	}
	for _, phase := range p.Phases {
		for _, task := range phase.Tasks {
			for _, dependency := range task.DependsOn {
				if dependency == task.Key {
					return fmt.Errorf("task %q cannot depend on itself", task.Key)
				}
				if _, ok := keys[dependency]; !ok {
					return fmt.Errorf("task %q has unknown dependency %q", task.Key, dependency)
				}
			}
		}
	}
	return nil
}

func phaseIsReady(p *Plan, phase *Phase) bool {
	for i := range p.Phases {
		if &p.Phases[i] == phase {
			return true
		}
		if !terminalWork(p.Phases[i].Status) {
			return false
		}
	}
	return false
}

func dependenciesComplete(p *Plan, task *Task) bool {
	if len(task.DependsOn) == 0 {
		return true
	}
	statuses := map[string]Status{}
	for _, phase := range p.Phases {
		for _, candidate := range phase.Tasks {
			statuses[candidate.Key] = candidate.Status
		}
	}
	for _, dependency := range task.DependsOn {
		status := statuses[dependency]
		if status != StatusCompleted && status != StatusSkipped {
			return false
		}
	}
	return true
}

func findPhase(p *Plan, id string) (*Phase, error) {
	id = strings.TrimSpace(id)
	for i := range p.Phases {
		if p.Phases[i].ID == id || p.Phases[i].Key == id {
			return &p.Phases[i], nil
		}
	}
	return nil, fmt.Errorf("phase %q not found", id)
}

func findTask(p *Plan, id string) (*Phase, *Task, error) {
	id = strings.TrimSpace(id)
	for i := range p.Phases {
		for j := range p.Phases[i].Tasks {
			if p.Phases[i].Tasks[j].ID == id || p.Phases[i].Tasks[j].Key == id {
				return &p.Phases[i], &p.Phases[i].Tasks[j], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("task %q not found", id)
}

func nextTaskID(p *Plan) string {
	return fmt.Sprintf("task-%d", taskCount(p)+1)
}

func taskCount(p *Plan) int {
	count := 0
	for _, phase := range p.Phases {
		count += len(phase.Tasks)
	}
	return count
}

func uniqueKey(requested, title string, existing map[string]struct{}) string {
	base := slug(requested)
	if base == "" {
		base = slug(title)
	}
	if base == "" {
		base = "item"
	}
	key := base
	for n := 2; ; n++ {
		if _, ok := existing[key]; !ok {
			existing[key] = struct{}{}
			return key
		}
		key = fmt.Sprintf("%s-%d", base, n)
	}
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func cleanStrings(values []string, max int) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == max {
			break
		}
	}
	return result
}

func appendLimited(values []string, value string, max int) []string {
	values = append(values, value)
	if len(values) > max {
		values = values[len(values)-max:]
	}
	return values
}

func normalizeProjectID(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return "", errors.New("project ID is required")
	}
	if strings.ContainsRune(value, '\x00') || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") {
		return "", errors.New("invalid project ID")
	}
	return value, nil
}

func validPlanStatus(status Status) bool {
	return status == StatusPlanned || status == StatusInProgress || status == StatusBlocked || status == StatusCompleted || status == StatusCancelled
}

func validWorkStatus(status Status) bool {
	return status == StatusPending || status == StatusInProgress || status == StatusBlocked || status == StatusCompleted || status == StatusSkipped || status == StatusCancelled
}

func terminalPlan(status Status) bool { return status == StatusCompleted || status == StatusCancelled }
func terminalWork(status Status) bool {
	return status == StatusCompleted || status == StatusSkipped || status == StatusCancelled
}

func allTasksTerminal(phase *Phase) bool {
	for _, task := range phase.Tasks {
		if !terminalWork(task.Status) {
			return false
		}
	}
	return true
}

func allPhasesTerminal(p *Plan) bool {
	for _, phase := range p.Phases {
		if !terminalWork(phase.Status) || !allTasksTerminal(&phase) {
			return false
		}
	}
	return true
}

func newID(prefix string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(buf[:]))
}
