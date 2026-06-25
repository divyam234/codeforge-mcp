package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Workspace struct {
	Root             string
	MaxFileBytes     int64
	MaxSearchResults int
	MaxTreeEntries   int
	AllowDelete      bool
}

type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

type SearchMatch struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	LineHash string `json:"line_hash"`
	Preview  string `json:"preview"`
}

func (w *Workspace) Resolve(rel string, mustExist bool) (string, error) {
	if strings.ContainsRune(rel, '\x00') {
		return "", errors.New("path contains NUL")
	}
	if rel == "" || rel == "." {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	candidate := filepath.Join(w.Root, clean)

	// Resolve the nearest existing ancestor. This protects both existing paths
	// and yet-to-be-created files from symlink traversal outside the workspace.
	check := candidate
	for {
		_, err := os.Lstat(check)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(check)
			if err != nil {
				return "", fmt.Errorf("resolve symlinks: %w", err)
			}
			if !isWithin(w.Root, resolved) {
				return "", errors.New("path resolves outside workspace")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(check)
		if parent == check {
			return "", errors.New("unable to resolve workspace path")
		}
		check = parent
	}
	if mustExist {
		if _, err := os.Lstat(candidate); err != nil {
			return "", err
		}
	}
	return candidate, nil
}

func isWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *Workspace) Relative(abs string) string {
	rel, err := filepath.Rel(w.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

func (w *Workspace) Tree(path string, depth, maxEntries int, includeHidden bool) ([]TreeEntry, bool, error) {
	if depth < 0 || depth > 20 {
		return nil, false, errors.New("depth must be between 0 and 20")
	}
	if maxEntries <= 0 || maxEntries > w.MaxTreeEntries {
		maxEntries = w.MaxTreeEntries
	}
	base, err := w.Resolve(path, true)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(base)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, errors.New("tree path is not a directory")
	}

	entries := make([]TreeEntry, 0, min(maxEntries, 128))
	truncated := false
	err = filepath.WalkDir(base, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == base {
			return nil
		}
		relToBase, err := filepath.Rel(base, current)
		if err != nil {
			return err
		}
		currentDepth := len(strings.Split(filepath.Clean(relToBase), string(filepath.Separator)))
		name := d.Name()
		if d.IsDir() && shouldSkipDir(name, includeHidden) {
			return filepath.SkipDir
		}
		if !includeHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		if currentDepth > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= maxEntries {
			truncated = true
			return fs.SkipAll
		}
		entry := TreeEntry{Path: w.Relative(current), Type: "file"}
		entryInfo, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			entry.Type = "symlink"
		case d.IsDir():
			entry.Type = "directory"
		default:
			entry.Size = entryInfo.Size()
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, truncated, err
}

func (w *Workspace) Find(pattern, path string, includeHidden bool, maxResults int) ([]string, bool, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, false, errors.New("pattern is required")
	}
	if maxResults <= 0 || maxResults > w.MaxSearchResults {
		maxResults = w.MaxSearchResults
	}
	matcher, err := compileGlob(pattern)
	if err != nil {
		return nil, false, err
	}
	base, err := w.Resolve(path, true)
	if err != nil {
		return nil, false, err
	}
	var matches []string
	truncated := false
	err = filepath.WalkDir(base, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if current != base && shouldSkipDir(d.Name(), includeHidden) {
				return filepath.SkipDir
			}
			return nil
		}
		if !includeHidden && strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel := w.Relative(current)
		if matcher.MatchString(rel) || matcher.MatchString(d.Name()) {
			if len(matches) >= maxResults {
				truncated = true
				return fs.SkipAll
			}
			matches = append(matches, rel)
		}
		return nil
	})
	sort.Strings(matches)
	return matches, truncated, err
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					out.WriteString("(?:.*/)?")
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	out.WriteString("$")
	re, err := regexp.Compile(out.String())
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}
	return re, nil
}

func (w *Workspace) Move(from, to string, overwrite bool) error {
	src, err := w.Resolve(from, true)
	if err != nil {
		return err
	}
	dst, err := w.Resolve(to, false)
	if err != nil {
		return err
	}
	if src == w.Root || dst == w.Root {
		return errors.New("workspace root cannot be moved or replaced")
	}
	if !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			return errors.New("destination exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if _, err := os.Lstat(dst); err == nil {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func (w *Workspace) Delete(path string, recursive bool) error {
	if !w.AllowDelete {
		return errors.New("deletion is disabled; set CODEFORGE_ALLOW_DELETE=true")
	}
	abs, err := w.Resolve(path, true)
	if err != nil {
		return err
	}
	if abs == w.Root {
		return errors.New("workspace root cannot be deleted")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() && !recursive {
		return os.Remove(abs)
	}
	return os.RemoveAll(abs)
}

func shouldSkipDir(name string, includeHidden bool) bool {
	always := map[string]struct{}{".git": {}, ".hg": {}, ".svn": {}, "node_modules": {}, "target": {}, "dist": {}, "build": {}, ".idea": {}}
	if _, ok := always[name]; ok {
		return true
	}
	return !includeHidden && strings.HasPrefix(name, ".")
}

func trimPreview(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
