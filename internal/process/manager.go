package process

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	root           string
	policy         string
	allowed        map[string]struct{}
	shell          string
	defaultTimeout time.Duration
	maxOutput      int
	sem            chan struct{}
	mu             sync.RWMutex
	processes      map[string]*running
}

type StartRequest struct {
	Command        string            `json:"command,omitempty" jsonschema:"Shell command string. Use only when shell features are needed."`
	Argv           []string          `json:"argv,omitempty" jsonschema:"Executable and arguments. Prefer this for simple commands."`
	CWD            string            `json:"cwd,omitempty" jsonschema:"Working directory relative to the active project."`
	Environment    map[string]string `json:"environment,omitempty" jsonschema:"Additional environment variables for this command."`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" jsonschema:"Hard timeout in seconds, up to 3600."`
	BaseDir        string            `json:"-"`
}

type StartResult struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	CWD       string `json:"cwd"`
	Command   string `json:"command"`
}

type RunResult struct {
	Mode             string `json:"mode"`
	Status           string `json:"status"`
	Command          string `json:"command"`
	CWD              string `json:"cwd"`
	Output           string `json:"output"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	Truncated        bool   `json:"truncated"`
	SessionID        string `json:"session_id,omitempty"`
	NextCursor       int64  `json:"next_cursor,omitempty"`
	OutputBaseCursor int64  `json:"output_base_cursor,omitempty"`
}

type PollResult struct {
	SessionID        string `json:"session_id"`
	Status           string `json:"status"`
	Output           string `json:"output"`
	OutputBaseCursor int64  `json:"output_base_cursor"`
	NextCursor       int64  `json:"next_cursor"`
	CursorWasStale   bool   `json:"cursor_was_stale"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	Truncated        bool   `json:"truncated"`
}

type ProcessSummary struct {
	SessionID  string `json:"session_id"`
	Status     string `json:"status"`
	Command    string `json:"command"`
	CWD        string `json:"cwd"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

type running struct {
	id       string
	command  string
	cwd      string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stdin    io.WriteCloser
	output   *limitedBuffer
	started  time.Time
	finished time.Time
	done     chan struct{}
	mu       sync.RWMutex
	status   string
	exitCode *int
}

type limitedBuffer struct {
	mu        sync.RWMutex
	data      []byte
	base      int64
	max       int
	truncated bool
}

func NewManager(root, policy string, allowed map[string]struct{}, shell string, maxConcurrent, maxOutput int, timeout time.Duration) *Manager {
	return &Manager{
		root: root, policy: policy, allowed: allowed, shell: shell,
		sem: make(chan struct{}, maxConcurrent), maxOutput: maxOutput,
		defaultTimeout: timeout, processes: make(map[string]*running),
	}
}

func (m *Manager) Policy() string { return m.policy }

func (m *Manager) AllowedCommands() []string {
	commands := make([]string, 0, len(m.allowed))
	for command := range m.allowed {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

func (m *Manager) Run(req StartRequest, yield time.Duration) (RunResult, error) {
	if yield < 0 {
		return RunResult{}, errors.New("foreground yield cannot be negative")
	}
	if yield == 0 {
		yield = 10 * time.Second
	}
	if yield > 30*time.Second {
		return RunResult{}, errors.New("foreground yield cannot exceed 30 seconds")
	}
	start, err := m.Start(req)
	if err != nil {
		return RunResult{}, err
	}
	p, err := m.get(start.SessionID)
	if err != nil {
		return RunResult{}, err
	}
	timer := time.NewTimer(yield)
	defer timer.Stop()
	select {
	case <-p.done:
		poll, err := m.Poll(start.SessionID, 0)
		if err != nil {
			return RunResult{}, err
		}
		_ = m.Forget(start.SessionID)
		return RunResult{
			Mode: "completed", Status: poll.Status, Command: start.Command, CWD: start.CWD,
			Output: poll.Output, ExitCode: poll.ExitCode, DurationMS: poll.DurationMS, Truncated: poll.Truncated,
		}, nil
	case <-timer.C:
		poll, err := m.Poll(start.SessionID, 0)
		if err != nil {
			return RunResult{}, err
		}
		return RunResult{
			Mode: "background", Status: poll.Status, Command: start.Command, CWD: start.CWD,
			Output: poll.Output, ExitCode: poll.ExitCode, DurationMS: poll.DurationMS, Truncated: poll.Truncated,
			SessionID: start.SessionID, NextCursor: poll.NextCursor, OutputBaseCursor: poll.OutputBaseCursor,
		}, nil
	}
}

func (m *Manager) Start(req StartRequest) (StartResult, error) {
	hasCommand := strings.TrimSpace(req.Command) != ""
	hasArgv := len(req.Argv) > 0
	if hasCommand == hasArgv {
		return StartResult{}, errors.New("provide exactly one of command or argv")
	}
	base, err := m.resolveBase(req.BaseDir)
	if err != nil {
		return StartResult{}, err
	}
	cwd, err := m.resolveCWD(base, req.CWD)
	if err != nil {
		return StartResult{}, err
	}
	select {
	case m.sem <- struct{}{}:
	default:
		return StartResult{}, errors.New("maximum concurrent process limit reached")
	}
	release := true
	defer func() {
		if release {
			<-m.sem
		}
	}()

	if req.TimeoutSeconds < 0 {
		return StartResult{}, errors.New("timeout_seconds cannot be negative")
	}
	for key := range req.Environment {
		if !validEnvKey(key) {
			return StartResult{}, fmt.Errorf("invalid environment variable name %q", key)
		}
	}
	timeout := m.defaultTimeout
	if req.TimeoutSeconds > 0 {
		if req.TimeoutSeconds > 3600 {
			return StartResult{}, errors.New("timeout_seconds cannot exceed 3600")
		}
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd, display, err := m.command(ctx, req)
	if err != nil {
		cancel()
		return StartResult{}, err
	}
	cmd.Dir = cwd
	cmd.Env = mergeEnvironment(os.Environ(), req.Environment)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error { return killProcess(cmd) }
	cmd.WaitDelay = 2 * time.Second
	buffer := &limitedBuffer{max: m.maxOutput}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return StartResult{}, err
	}
	id, err := randomID()
	if err != nil {
		cancel()
		return StartResult{}, err
	}
	p := &running{
		id: id, command: display, cwd: m.relative(cwd), cmd: cmd, cancel: cancel,
		stdin: stdin, output: buffer, started: time.Now().UTC(), status: "running", done: make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return StartResult{}, err
	}
	m.mu.Lock()
	m.processes[id] = p
	m.mu.Unlock()
	release = false
	go m.wait(ctx, p)
	return StartResult{SessionID: id, Status: "running", CWD: p.cwd, Command: display}, nil
}

func (m *Manager) command(ctx context.Context, req StartRequest) (*exec.Cmd, string, error) {
	if strings.TrimSpace(req.Command) != "" {
		if strings.ContainsRune(req.Command, '\x00') {
			return nil, "", errors.New("command contains NUL")
		}
		if m.policy == "allowlist" {
			return nil, "", errors.New("shell command strings are disabled by allowlist policy; use argv")
		}
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, m.shell, "/d", "/s", "/c", req.Command), req.Command, nil
		}
		return exec.CommandContext(ctx, m.shell, "-lc", req.Command), req.Command, nil
	}
	if strings.TrimSpace(req.Argv[0]) == "" {
		return nil, "", errors.New("argv[0] is empty")
	}
	for _, arg := range req.Argv {
		if strings.ContainsRune(arg, '\x00') {
			return nil, "", errors.New("argv contains NUL")
		}
	}
	if m.policy == "allowlist" {
		binary := filepath.Base(req.Argv[0])
		if binary != req.Argv[0] || strings.ContainsAny(req.Argv[0], `/\`) {
			return nil, "", errors.New("allowlist mode requires argv[0] to be a bare executable name")
		}
		if _, ok := m.allowed[binary]; !ok {
			return nil, "", fmt.Errorf("command %q is not allowed", binary)
		}
	}
	return exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...), quoteArgs(req.Argv), nil
}

func (m *Manager) wait(ctx context.Context, p *running) {
	err := p.cmd.Wait()
	_ = p.stdin.Close()
	code := 0
	status := "succeeded"
	if err != nil {
		status = "failed"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = "timed_out"
	}
	p.mu.Lock()
	if p.status != "cancelled" {
		p.status = status
	}
	p.exitCode = &code
	p.finished = time.Now().UTC()
	p.mu.Unlock()
	p.cancel()
	close(p.done)
	<-m.sem
}

func (m *Manager) Poll(id string, cursor int64) (PollResult, error) {
	if cursor < 0 {
		return PollResult{}, errors.New("cursor cannot be negative")
	}
	p, err := m.get(id)
	if err != nil {
		return PollResult{}, err
	}
	output, base, next, stale, truncated := p.output.read(cursor)
	p.mu.RLock()
	defer p.mu.RUnlock()
	end := time.Now().UTC()
	if !p.finished.IsZero() {
		end = p.finished
	}
	result := PollResult{
		SessionID: id, Status: p.status, Output: output, OutputBaseCursor: base,
		NextCursor: next, CursorWasStale: stale, ExitCode: p.exitCode,
		StartedAt: p.started.Format(time.RFC3339Nano), DurationMS: end.Sub(p.started).Milliseconds(),
		Truncated: truncated,
	}
	if !p.finished.IsZero() {
		result.FinishedAt = p.finished.Format(time.RFC3339Nano)
	}
	return result, nil
}

func (m *Manager) List() []ProcessSummary {
	m.mu.RLock()
	items := make([]*running, 0, len(m.processes))
	for _, p := range m.processes {
		items = append(items, p)
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].started.After(items[j].started) })
	result := make([]ProcessSummary, 0, len(items))
	for _, p := range items {
		p.mu.RLock()
		summary := ProcessSummary{SessionID: p.id, Status: p.status, Command: p.command, CWD: p.cwd, StartedAt: p.started.Format(time.RFC3339Nano)}
		if !p.finished.IsZero() {
			summary.FinishedAt = p.finished.Format(time.RFC3339Nano)
		}
		p.mu.RUnlock()
		result = append(result, summary)
	}
	return result
}

func (m *Manager) Forget(id string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.RLock()
	running := p.status == "running"
	p.mu.RUnlock()
	if running {
		return errors.New("cannot forget a running process")
	}
	m.mu.Lock()
	delete(m.processes, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) WriteStdin(id, input string, closeAfter bool) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.RLock()
	status := p.status
	p.mu.RUnlock()
	if status != "running" {
		return errors.New("process is not running")
	}
	if input != "" {
		if _, err := io.WriteString(p.stdin, input); err != nil {
			return err
		}
	}
	if closeAfter {
		return p.stdin.Close()
	}
	return nil
}

func (m *Manager) Cancel(id string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.status != "running" {
		p.mu.Unlock()
		return nil
	}
	p.status = "cancelled"
	p.mu.Unlock()
	_ = killProcess(p.cmd)
	p.cancel()
	return nil
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	if runtime.GOOS != "windows" {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd.Process.Kill()
}

func (m *Manager) get(id string) (*running, error) {
	m.mu.RLock()
	p := m.processes[id]
	m.mu.RUnlock()
	if p == nil {
		return nil, errors.New("unknown process session")
	}
	return p, nil
}

func (m *Manager) resolveBase(base string) (string, error) {
	if base == "" {
		base = m.root
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("internal base directory must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	if !within(m.root, resolved) {
		return "", errors.New("base directory escapes workspace root")
	}
	return resolved, nil
}

func (m *Manager) resolveCWD(base, rel string) (string, error) {
	if rel == "" || rel == "." {
		return base, nil
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("cwd must be relative to active project")
	}
	candidate := filepath.Join(base, filepath.Clean(rel))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !within(base, resolved) {
		return "", errors.New("cwd escapes active project")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("cwd is not a directory")
	}
	return resolved, nil
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Manager) relative(abs string) string {
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func mergeEnvironment(base []string, custom map[string]string) []string {
	values := make(map[string]string)
	for _, item := range base {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	for key, value := range custom {
		if validEnvKey(key) {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func validEnvKey(key string) bool {
	if key == "" || strings.ContainsAny(key, "=\x00") {
		return false
	}
	for i, r := range key {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "proc_" + hex.EncodeToString(value[:]), nil
}

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'") {
			quoted[i] = fmt.Sprintf("%q", arg)
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		drop := len(b.data) - b.max
		b.data = append([]byte(nil), b.data[drop:]...)
		b.base += int64(drop)
		b.truncated = true
	}
	return original, nil
}

func (b *limitedBuffer) read(cursor int64) (string, int64, int64, bool, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	stale := cursor < b.base
	if cursor < b.base {
		cursor = b.base
	}
	end := b.base + int64(len(b.data))
	if cursor > end {
		cursor = end
	}
	start := cursor - b.base
	return string(bytes.Clone(b.data[start:])), b.base, end, stale, b.truncated
}
