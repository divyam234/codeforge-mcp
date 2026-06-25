package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchCheckAndApply(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "old\n")
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"
	result, err := w.ApplyPatch(patch, true)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("check failed: %+v %v", result, err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "old\n" {
		t.Fatalf("check modified file: %q", data)
	}
	result, err = w.ApplyPatch(patch, false)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("apply failed: %+v %v", result, err)
	}
	data, _ = os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "new\n" {
		t.Fatalf("patch not applied: %q", data)
	}
}

func TestApplyPatchRejectsUnsafeAndInvalidPatch(t *testing.T) {
	w := testWorkspace(t)
	unsafe := "--- a/../outside\n+++ b/../outside\n@@ -0,0 +1 @@\n+x\n"
	if _, err := w.ApplyPatch(unsafe, false); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe path rejection, got %v", err)
	}
	if _, err := w.ApplyPatch("not a patch", false); err == nil || !strings.Contains(err.Error(), "no file paths") {
		t.Fatalf("expected invalid patch rejection, got %v", err)
	}
}

func TestGitStatusAndDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	w := testWorkspace(t)
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		if result, err := w.Git(args...); err != nil {
			t.Fatalf("git %v: %+v %v", args, result, err)
		}
	}
	writeTestFile(t, w, "a.txt", "one\n")
	if result, err := w.Git("add", "a.txt"); err != nil {
		t.Fatalf("git add: %+v %v", result, err)
	}
	if result, err := w.Git("commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %+v %v", result, err)
	}
	writeTestFile(t, w, "a.txt", "two\n")
	status, err := w.Git("status", "--porcelain")
	if err != nil || !strings.Contains(status.Output, "M a.txt") {
		t.Fatalf("unexpected status: %+v %v", status, err)
	}
	diff, err := w.Git("diff", "--", "a.txt")
	if err != nil || !strings.Contains(diff.Output, "+two") {
		t.Fatalf("unexpected diff: %+v %v", diff, err)
	}
}
