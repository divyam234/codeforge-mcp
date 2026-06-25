package process

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T, policy string, maxOutput int) *Manager {
	t.Helper()
	return NewManager(t.TempDir(), policy, map[string]struct{}{"printf": {}, "sh": {}}, "/bin/sh", 3, maxOutput, 2*time.Second)
}

func waitFinished(t *testing.T, m *Manager, id string) PollResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := m.Poll(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "running" {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process did not finish")
	return PollResult{}
}

func TestRunCompletesShortCommandInForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	m := newTestManager(t, "unrestricted", 1<<20)
	result, err := m.Run(StartRequest{Command: "printf hello"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "completed" || result.Status != "succeeded" || result.Output != "hello" || result.SessionID != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(m.List()) != 0 {
		t.Fatal("foreground command should not retain a process session")
	}
}

func TestRunPromotesLongCommandToBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	m := newTestManager(t, "unrestricted", 1<<20)
	result, err := m.Run(StartRequest{Command: "printf start; sleep 0.2; printf end"}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "background" || result.SessionID == "" {
		t.Fatalf("unexpected promoted result: %+v", result)
	}
	poll := waitFinished(t, m, result.SessionID)
	if poll.Status != "succeeded" || !strings.Contains(poll.Output, "end") {
		t.Fatalf("unexpected final result: %+v", poll)
	}
}

func TestDirectArgvEnvironmentAndBaseDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	m := newTestManager(t, "unrestricted", 1<<20)
	result, err := m.Run(StartRequest{Argv: []string{"sh", "-c", "printf '%s' \"$VALUE\""}, Environment: map[string]string{"VALUE": "ok"}}, time.Second)
	if err != nil || result.Output != "ok" {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestWriteStdinTimeoutAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	m := newTestManager(t, "unrestricted", 1<<20)
	start, err := m.Start(StartRequest{Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteStdin(start.SessionID, "hello\n", true); err != nil {
		t.Fatal(err)
	}
	if got := waitFinished(t, m, start.SessionID); got.Output != "hello\n" {
		t.Fatalf("unexpected output: %+v", got)
	}

	timed, err := m.Start(StartRequest{Command: "sleep 2", TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFinished(t, m, timed.SessionID); got.Status != "timed_out" {
		t.Fatalf("unexpected timeout: %+v", got)
	}

	cancelled, err := m.Start(StartRequest{Command: "sleep 5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(cancelled.SessionID); err != nil {
		t.Fatal(err)
	}
	if got := waitFinished(t, m, cancelled.SessionID); got.Status != "cancelled" {
		t.Fatalf("unexpected cancel: %+v", got)
	}
}

func TestOutputTruncationAllowlistAndValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command")
	}
	m := newTestManager(t, "unrestricted", 8)
	result, err := m.Run(StartRequest{Command: "printf 1234567890"}, time.Second)
	if err != nil || !result.Truncated || result.Output != "34567890" {
		t.Fatalf("unexpected truncation: %+v, %v", result, err)
	}

	allowed := newTestManager(t, "allowlist", 1<<20)
	if _, err := allowed.Start(StartRequest{Command: "printf bad"}); err == nil {
		t.Fatal("expected shell command rejection")
	}
	if _, err := allowed.Run(StartRequest{Argv: []string{"printf", "ok"}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := allowed.Start(StartRequest{}); err == nil {
		t.Fatal("expected command validation")
	}
}

func TestRejectsNegativeTimingAndInvalidEnvironment(t *testing.T) {
	m := newTestManager(t, "unrestricted", 1<<20)
	if _, err := m.Run(StartRequest{Command: "true"}, -time.Millisecond); err == nil {
		t.Fatal("expected negative foreground yield rejection")
	}
	if _, err := m.Start(StartRequest{Command: "true", TimeoutSeconds: -1}); err == nil {
		t.Fatal("expected negative timeout rejection")
	}
	if _, err := m.Start(StartRequest{Command: "true", Environment: map[string]string{"BAD-NAME": "x"}}); err == nil {
		t.Fatal("expected invalid environment name rejection")
	}
}
