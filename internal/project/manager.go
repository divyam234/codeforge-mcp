package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/killercrock/codeforge-mcp/internal/workspace"
)

type Limits struct {
	MaxFileBytes     int64
	MaxSearchResults int
	MaxTreeEntries   int
	AllowDelete      bool
}

type Manager struct {
	root   string
	limits Limits

	mu     sync.RWMutex
	active string
}

type Summary struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Active         bool     `json:"active"`
	GitInitialized bool     `json:"git_initialized"`
	Markers        []string `json:"markers,omitempty"`
	Languages      []string `json:"languages,omitempty"`
}

type CreateRequest struct {
	Name         string `json:"name" jsonschema:"Human-readable project name."`
	Directory    string `json:"directory,omitempty" jsonschema:"Relative project directory. Defaults to a slug derived from name."`
	Template     string `json:"template,omitempty" jsonschema:"Project template: empty, go, rust, node, or python."`
	Module       string `json:"module,omitempty" jsonschema:"Go module path or package identifier used by supported templates."`
	GitInit      *bool  `json:"git_init,omitempty" jsonschema:"Initialize a Git repository. Defaults to true."`
	CreateReadme *bool  `json:"create_readme,omitempty" jsonschema:"Create README.md. Defaults to true."`
	Select       *bool  `json:"select,omitempty" jsonschema:"Select the new project as active. Defaults to true."`
}

type CreateResult struct {
	Project        Summary  `json:"project"`
	CreatedFiles   []string `json:"created_files"`
	GitInitialized bool     `json:"git_initialized"`
	Selected       bool     `json:"selected"`
}

func NewManager(root, active string, limits Limits) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	m := &Manager{root: resolved, limits: limits}
	if strings.TrimSpace(active) != "" {
		if _, err := m.resolveProject(active, true); err != nil {
			return nil, fmt.Errorf("active project: %w", err)
		}
		m.active = normalizeID(active)
	} else {
		m.active = m.detectDefaultActive()
	}
	return m, nil
}

func (m *Manager) Root() string { return m.root }

func (m *Manager) ActiveID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

func (m *Manager) Active() (*workspace.Workspace, Summary, error) {
	m.mu.RLock()
	id := m.active
	m.mu.RUnlock()
	if id == "" {
		return nil, Summary{}, errors.New("no active project; call project_list then project_select, or create a project")
	}
	path, err := m.resolveProject(id, true)
	if err != nil {
		return nil, Summary{}, err
	}
	summary, err := m.summarize(id, path, true)
	if err != nil {
		return nil, Summary{}, err
	}
	return &workspace.Workspace{
		Root:             path,
		MaxFileBytes:     m.limits.MaxFileBytes,
		MaxSearchResults: m.limits.MaxSearchResults,
		MaxTreeEntries:   m.limits.MaxTreeEntries,
		AllowDelete:      m.limits.AllowDelete,
	}, summary, nil
}

func (m *Manager) List() ([]Summary, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return nil, err
	}
	active := m.ActiveID()
	var result []Summary
	if looksLikeProject(m.root) {
		s, err := m.summarize(".", m.root, active == ".")
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := filepath.ToSlash(entry.Name())
		path, err := m.resolveProject(id, true)
		if err != nil {
			continue
		}
		s, err := m.summarize(id, path, active == id)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (m *Manager) Select(id string) (Summary, error) {
	path, err := m.resolveProject(id, true)
	if err != nil {
		return Summary{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Summary{}, errors.New("project is not a directory")
	}
	id = normalizeID(id)
	m.mu.Lock()
	m.active = id
	m.mu.Unlock()
	return m.summarize(id, path, true)
}

func (m *Manager) Create(req CreateRequest) (result CreateResult, err error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return result, errors.New("name is required")
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return result, errors.New("name must be a single line without NUL bytes")
	}
	directory := strings.TrimSpace(req.Directory)
	if directory == "" {
		directory = slug(name)
	}
	if directory == "" {
		return result, errors.New("could not derive a project directory from name")
	}
	id := normalizeID(directory)
	if id == "." {
		return result, errors.New("project directory must not be the workspace root")
	}
	if filepath.Dir(filepath.FromSlash(id)) != "." {
		return result, errors.New("project directory must be one direct child of the workspace root")
	}
	path, err := m.resolveProject(id, false)
	if err != nil {
		return result, err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return result, errors.New("project directory already exists")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(path)
		}
	}()

	if strings.ContainsAny(req.Module, "\r\n\x00") {
		return result, errors.New("module must be a single line without NUL bytes")
	}
	template := strings.ToLower(strings.TrimSpace(req.Template))
	if template == "" {
		template = "empty"
	}
	created, err := createTemplate(path, name, template, strings.TrimSpace(req.Module), boolDefault(req.CreateReadme, true))
	if err != nil {
		return result, err
	}
	gitInitialized := false
	if boolDefault(req.GitInit, true) {
		cmd := exec.Command("git", "init", "--quiet")
		cmd.Dir = path
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Run(); err != nil {
			return result, fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(output.String()))
		}
		gitInitialized = true
	}
	selected := boolDefault(req.Select, true)
	if selected {
		m.mu.Lock()
		m.active = id
		m.mu.Unlock()
	}
	summary, err := m.summarize(id, path, selected)
	if err != nil {
		return result, err
	}
	committed = true
	return CreateResult{Project: summary, CreatedFiles: created, GitInitialized: gitInitialized, Selected: selected}, nil
}

func (m *Manager) resolveProject(id string, mustExist bool) (string, error) {
	if strings.ContainsRune(id, '\x00') {
		return "", errors.New("project id contains NUL")
	}
	id = normalizeID(id)
	if filepath.IsAbs(id) {
		return "", errors.New("project id must be relative")
	}
	clean := filepath.Clean(id)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("project path escapes workspace root")
	}
	candidate := filepath.Join(m.root, clean)
	check := candidate
	for {
		_, err := os.Lstat(check)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(check)
			if err != nil {
				return "", err
			}
			if !within(m.root, resolved) {
				return "", errors.New("project resolves outside workspace root")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(check)
		if parent == check {
			return "", errors.New("unable to resolve project path")
		}
		check = parent
	}
	if mustExist {
		info, err := os.Stat(candidate)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("project is not a directory")
		}
	}
	return candidate, nil
}

func (m *Manager) summarize(id, path string, active bool) (Summary, error) {
	markers, languages := detectMarkers(path)
	return Summary{
		ID:             normalizeID(id),
		Name:           projectName(id, path),
		Path:           normalizeID(id),
		Active:         active,
		GitInitialized: exists(filepath.Join(path, ".git")),
		Markers:        markers,
		Languages:      languages,
	}, nil
}

func (m *Manager) detectDefaultActive() string {
	if looksLikeProject(m.root) {
		return "."
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 1 {
		return filepath.ToSlash(dirs[0])
	}
	return ""
}

func createTemplate(root, name, template, module string, readme bool) ([]string, error) {
	files := map[string]string{}
	if readme {
		files["README.md"] = "# " + name + "\n"
	}
	switch template {
	case "empty":
	case "go":
		if module == "" {
			module = slug(name)
		}
		files["go.mod"] = "module " + module + "\n\ngo 1.24\n"
		files["main.go"] = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello from " + escapeGo(name) + "\")\n}\n"
		files[".gitignore"] = "/bin/\n*.test\ncoverage.out\n"
	case "rust":
		pkg := strings.ReplaceAll(slug(name), "-", "_")
		if pkg == "" {
			pkg = "app"
		}
		files["Cargo.toml"] = "[package]\nname = \"" + pkg + "\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dependencies]\n"
		files["src/main.rs"] = "fn main() {\n    println!(\"Hello from " + escapeRust(name) + "\");\n}\n"
		files[".gitignore"] = "/target/\n"
	case "node":
		pkg := slug(name)
		if pkg == "" {
			pkg = "app"
		}
		files["package.json"] = "{\n  \"name\": \"" + pkg + "\",\n  \"version\": \"0.1.0\",\n  \"private\": true,\n  \"type\": \"module\",\n  \"scripts\": {\n    \"start\": \"node src/index.js\",\n    \"test\": \"node --test\"\n  }\n}\n"
		files["src/index.js"] = "console.log(\"Hello from " + escapeJS(name) + "\");\n"
		files[".gitignore"] = "node_modules/\ncoverage/\n"
	case "python":
		pkg := strings.ReplaceAll(slug(name), "-", "_")
		if pkg == "" {
			pkg = "app"
		}
		files["pyproject.toml"] = "[project]\nname = \"" + slug(name) + "\"\nversion = \"0.1.0\"\nrequires-python = \">=3.11\"\n\n[build-system]\nrequires = [\"hatchling\"]\nbuild-backend = \"hatchling.build\"\n"
		files["src/"+pkg+"/__init__.py"] = "\"\"\"" + name + ".\"\"\"\n"
		files["tests/.gitkeep"] = ""
		files[".gitignore"] = "__pycache__/\n.venv/\n.pytest_cache/\n"
	default:
		return nil, fmt.Errorf("unsupported template %q; use empty, go, rust, node, or python", template)
	}
	paths := make([]string, 0, len(files))
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths, nil
}

func detectMarkers(root string) ([]string, []string) {
	checks := []struct {
		file string
		lang string
	}{
		{"go.mod", "Go"}, {"Cargo.toml", "Rust"}, {"package.json", "JavaScript/TypeScript"},
		{"pyproject.toml", "Python"}, {"requirements.txt", "Python"}, {"pom.xml", "Java"},
		{"build.gradle", "Java/Kotlin"}, {"CMakeLists.txt", "C/C++"},
	}
	var markers, languages []string
	seen := map[string]bool{}
	for _, check := range checks {
		if exists(filepath.Join(root, check.file)) {
			markers = append(markers, check.file)
			if !seen[check.lang] {
				seen[check.lang] = true
				languages = append(languages, check.lang)
			}
		}
	}
	return markers, languages
}

func looksLikeProject(root string) bool {
	if exists(filepath.Join(root, ".git")) {
		return true
	}
	markers, _ := detectMarkers(root)
	return len(markers) > 0
}

func projectName(id, path string) string {
	if normalizeID(id) == "." {
		return filepath.Base(path)
	}
	return filepath.Base(filepath.FromSlash(id))
}

func normalizeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "." {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(id)))
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugRE.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func escapeGo(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value)
}
func escapeRust(value string) string { return escapeGo(value) }
func escapeJS(value string) string   { return escapeGo(value) }
