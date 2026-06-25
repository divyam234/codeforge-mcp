package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testWorkspace(t *testing.T) *Workspace {
	t.Helper()
	return &Workspace{Root: t.TempDir(), MaxFileBytes: 1 << 20, MaxSearchResults: 100, MaxTreeEntries: 100, AllowDelete: true}
}

func writeTestFile(t *testing.T, w *Workspace, path, content string) {
	t.Helper()
	abs := filepath.Join(w.Root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRejectsEscape(t *testing.T) {
	w := testWorkspace(t)
	for _, path := range []string{"../outside", "a/../../outside"} {
		if _, err := w.Resolve(path, false); err == nil {
			t.Fatalf("expected %q to fail", path)
		}
	}
	if _, err := w.Resolve(filepath.Join(string(filepath.Separator), "tmp"), false); err == nil {
		t.Fatal("expected absolute path to fail")
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	w := testWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(w.Root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := w.Resolve("link/secret.txt", false); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

func TestReadFileHashlineAndPlain(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "one\ntwo\nthree")

	result, err := w.ReadFile("a.txt", 2, 2, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalLines != 3 || !result.Truncated || result.StartLine != 2 || result.EndLine != 2 {
		t.Fatalf("unexpected metadata: %+v", result)
	}
	if !strings.HasPrefix(result.Content, "2:") || !strings.HasSuffix(result.Content, "|two") {
		t.Fatalf("unexpected hashline: %q", result.Content)
	}
	if len(result.Snapshot) != 64 {
		t.Fatalf("unexpected snapshot: %q", result.Snapshot)
	}

	plain, err := w.ReadFile("a.txt", 1, 0, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Content != "1\tone\n2\ttwo\n3\tthree" {
		t.Fatalf("unexpected plain output: %q", plain.Content)
	}
	if plain.FinalNewline {
		t.Fatal("file should not have final newline")
	}
}

func TestReadFileCRLFAndEmpty(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "crlf.txt", "one\r\ntwo\r\n")
	result, err := w.ReadFile("crlf.txt", 0, 0, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	if result.Newline != "crlf" || !result.FinalNewline || result.TotalLines != 2 {
		t.Fatalf("unexpected CRLF metadata: %+v", result)
	}
	writeTestFile(t, w, "empty.txt", "")
	empty, err := w.ReadFile("empty.txt", 0, 0, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	if empty.TotalLines != 0 || empty.Content != "" {
		t.Fatalf("unexpected empty read: %+v", empty)
	}
}

func TestWriteFileCreateOverwriteAndStaleSnapshot(t *testing.T) {
	w := testWorkspace(t)
	created, err := w.WriteFile("nested/a.txt", "hello\n", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Overwritten {
		t.Fatalf("unexpected create result: %+v", created)
	}
	if _, err := w.WriteFile("nested/a.txt", "again", "", true); err == nil {
		t.Fatal("create_only should reject existing file")
	}

	read, err := w.ReadFile("nested/a.txt", 0, 0, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := w.WriteFile("nested/a.txt", "updated", read.Snapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Overwritten {
		t.Fatalf("unexpected overwrite result: %+v", updated)
	}
	if _, err := w.WriteFile("nested/a.txt", "stale", read.Snapshot, false); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale snapshot error, got %v", err)
	}
}

func TestEditFileReplaceInsertDeleteAndMultipleEdits(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "one\ntwo\nthree\nfour\n")
	read, err := w.ReadFile("a.txt", 0, 0, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseHashlineOutput(t, read.Content)
	result, err := w.EditFile("a.txt", read.Snapshot, []HashEdit{
		{Mode: "insert_before", StartLine: 1, StartHash: lines[1], Replacement: "zero"},
		{Mode: "replace", StartLine: 2, StartHash: lines[2], Replacement: "TWO"},
		{Mode: "replace", StartLine: 3, StartHash: lines[3], EndLine: 4, EndHash: lines[4], Replacement: "three-and-four"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Edits != 3 || result.OldSnapshot == result.NewSnapshot {
		t.Fatalf("unexpected edit result: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "zero\none\nTWO\nthree-and-four\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	read2, err := w.ReadFile("a.txt", 0, 0, "hashline")
	if err != nil {
		t.Fatal(err)
	}
	lines2 := parseHashlineOutput(t, read2.Content)
	_, err = w.EditFile("a.txt", read2.Snapshot, []HashEdit{{Mode: "replace", StartLine: 2, StartHash: lines2[2], Replacement: ""}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "zero\nTWO\nthree-and-four\n" {
		t.Fatalf("delete failed: %q", data)
	}
}

func TestEditFileInsertAfterFinalLineWithoutNewline(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "one")
	read, _ := w.ReadFile("a.txt", 0, 0, "hashline")
	lines := parseHashlineOutput(t, read.Content)
	_, err := w.EditFile("a.txt", read.Snapshot, []HashEdit{{Mode: "insert_after", StartLine: 1, StartHash: lines[1], Replacement: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "one\ntwo" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestEditFilePreservesCRLF(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "one\r\ntwo\r\n")
	read, _ := w.ReadFile("a.txt", 0, 0, "hashline")
	lines := parseHashlineOutput(t, read.Content)
	_, err := w.EditFile("a.txt", read.Snapshot, []HashEdit{{Mode: "replace", StartLine: 2, StartHash: lines[2], Replacement: "TWO"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Root, "a.txt"))
	if string(data) != "one\r\nTWO\r\n" {
		t.Fatalf("CRLF was not preserved: %q", data)
	}
}

func TestEditFileRejectsStaleAnchorSnapshotAndOverlap(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "a.txt", "one\ntwo\nthree\n")
	read, _ := w.ReadFile("a.txt", 0, 0, "hashline")
	lines := parseHashlineOutput(t, read.Content)
	if _, err := w.EditFile("a.txt", "bad", []HashEdit{{StartLine: 1, StartHash: lines[1], Replacement: "x"}}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale snapshot, got %v", err)
	}
	if _, err := w.EditFile("a.txt", read.Snapshot, []HashEdit{{StartLine: 1, StartHash: "bad", Replacement: "x"}}); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("expected stale anchor, got %v", err)
	}
	_, err := w.EditFile("a.txt", read.Snapshot, []HashEdit{
		{StartLine: 1, StartHash: lines[1], EndLine: 2, EndHash: lines[2], Replacement: "x"},
		{StartLine: 2, StartHash: lines[2], Replacement: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestSearchFindTreeMoveDelete(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "pkg/a.go", "package pkg\nfunc Alpha() {}\n")
	writeTestFile(t, w, "pkg/b.txt", "alpha\n")
	writeTestFile(t, w, ".hidden/x.go", "Alpha\n")

	matches, truncated, err := w.Search("Alpha", "", "**/*.go", false, true, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(matches) != 1 || matches[0].Path != "pkg/a.go" || matches[0].LineHash == "" {
		t.Fatalf("unexpected search: %+v truncated=%v", matches, truncated)
	}
	files, _, err := w.Find("**/*.go", "", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "pkg/a.go" {
		t.Fatalf("unexpected find: %#v", files)
	}
	entries, _, err := w.Tree("", 3, 20, false)
	if err != nil || len(entries) < 3 {
		t.Fatalf("unexpected tree: %#v err=%v", entries, err)
	}
	if err := w.Move("pkg/b.txt", "pkg/c.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete("pkg/c.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "pkg/c.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestBinaryAndOversizedFilesRejected(t *testing.T) {
	w := testWorkspace(t)
	writeTestFile(t, w, "binary", string([]byte{'a', 0, 'b'}))
	if _, err := w.ReadFile("binary", 0, 0, "hashline"); err == nil {
		t.Fatal("expected binary rejection")
	}
	w.MaxFileBytes = 2
	writeTestFile(t, w, "large", "abc")
	if _, err := w.ReadFile("large", 0, 0, "hashline"); err == nil {
		t.Fatal("expected size rejection")
	}
}

func parseHashlineOutput(t *testing.T, content string) map[int]string {
	t.Helper()
	result := map[int]string{}
	for _, line := range strings.Split(content, "\n") {
		left, _, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("invalid hashline %q", line)
		}
		var n int
		var hash string
		if _, err := fmtSscanf(left, &n, &hash); err != nil {
			t.Fatalf("invalid anchor %q: %v", left, err)
		}
		result[n] = hash
	}
	return result
}

func fmtSscanf(value string, n *int, hash *string) (int, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return 0, os.ErrInvalid
	}
	var parsed int
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		parsed = parsed*10 + int(r-'0')
	}
	*n, *hash = parsed, parts[1]
	return 2, nil
}

func TestAtomicWritePreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode semantics differ on Windows")
	}
	w := testWorkspace(t)
	writeTestFile(t, w, "script.sh", "echo old\n")
	path := filepath.Join(w.Root, "script.sh")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	read, _ := w.ReadFile("script.sh", 0, 0, "hashline")
	if _, err := w.WriteFile("script.sh", "echo new\n", read.Snapshot, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}
}
