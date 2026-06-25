package project

import (
	"os"
	"path/filepath"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), "", Limits{MaxFileBytes: 1 << 20, MaxSearchResults: 100, MaxTreeEntries: 100, AllowDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateListSelectProjects(t *testing.T) {
	m := testManager(t)
	result, err := m.Create(CreateRequest{Name: "Demo API", Template: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.GitInitialized || !result.Selected || result.Project.ID != "demo-api" {
		t.Fatalf("unexpected create result: %+v", result)
	}
	for _, file := range []string{"README.md", "go.mod", "main.go", ".git"} {
		if _, err := os.Stat(filepath.Join(m.Root(), "demo-api", file)); err != nil {
			t.Fatalf("missing %s: %v", file, err)
		}
	}
	projects, err := m.List()
	if err != nil || len(projects) != 1 || !projects[0].Active {
		t.Fatalf("unexpected projects: %+v, %v", projects, err)
	}
	if _, _, err := m.Active(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTemplatesAndDefaults(t *testing.T) {
	m := testManager(t)
	falseValue := false
	for _, template := range []string{"empty", "rust", "node", "python"} {
		result, err := m.Create(CreateRequest{Name: "P " + template, Directory: template, Template: template, GitInit: &falseValue, Select: &falseValue})
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		if result.GitInitialized || result.Selected {
			t.Fatalf("unexpected defaults override: %+v", result)
		}
	}
}

func TestProjectPathSafetyAndDuplicate(t *testing.T) {
	m := testManager(t)
	if _, err := m.Create(CreateRequest{Name: "bad", Directory: "../bad"}); err == nil {
		t.Fatal("expected path escape rejection")
	}
	if _, err := m.Create(CreateRequest{Name: "demo", Directory: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(CreateRequest{Name: "demo", Directory: "demo"}); err == nil {
		t.Fatal("expected duplicate rejection")
	}
}

func TestDefaultActiveForRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(root, "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveID() != "." {
		t.Fatalf("got active %q", m.ActiveID())
	}
}

func TestCreateRejectsNestedAndMultilineMetadata(t *testing.T) {
	m := testManager(t)
	if _, err := m.Create(CreateRequest{Name: "nested", Directory: "group/nested"}); err == nil {
		t.Fatal("expected nested project rejection")
	}
	if _, err := m.Create(CreateRequest{Name: "bad\nname", Directory: "bad"}); err == nil {
		t.Fatal("expected multiline name rejection")
	}
	if _, err := m.Create(CreateRequest{Name: "bad module", Directory: "bad-module", Template: "go", Module: "bad\nmodule"}); err == nil {
		t.Fatal("expected multiline module rejection")
	}
}

func TestCreateRollbackOnTemplateError(t *testing.T) {
	m := testManager(t)
	if _, err := m.Create(CreateRequest{Name: "Broken", Directory: "broken", Template: "unknown"}); err == nil {
		t.Fatal("expected template error")
	}
	if _, err := os.Stat(filepath.Join(m.Root(), "broken")); !os.IsNotExist(err) {
		t.Fatalf("failed creation was not rolled back: %v", err)
	}
}
