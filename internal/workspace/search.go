package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (w *Workspace) Search(query, path, glob string, regexMode, caseSensitive, includeHidden bool, maxResults int) ([]SearchMatch, bool, error) {
	if query == "" {
		return nil, false, errors.New("query is required")
	}
	if maxResults <= 0 || maxResults > w.MaxSearchResults {
		maxResults = w.MaxSearchResults
	}
	base, err := w.Resolve(path, true)
	if err != nil {
		return nil, false, err
	}
	pattern := regexp.QuoteMeta(query)
	if regexMode {
		pattern = query
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, false, fmt.Errorf("invalid regular expression: %w", err)
	}
	var globRE *regexp.Regexp
	if strings.TrimSpace(glob) != "" {
		globRE, err = compileGlob(glob)
		if err != nil {
			return nil, false, err
		}
	}
	matches := make([]SearchMatch, 0, min(maxResults, 64))
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
		if globRE != nil && !globRE.MatchString(rel) && !globRE.MatchString(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > w.MaxFileBytes {
			return nil
		}
		file, err := os.Open(current)
		if err != nil {
			return nil
		}
		stop, scanErr := scanFile(file, re, rel, maxResults-len(matches), &matches)
		_ = file.Close()
		if scanErr != nil {
			return nil
		}
		if stop || len(matches) >= maxResults {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	return matches, truncated, err
}

func scanFile(file *os.File, re *regexp.Regexp, path string, remaining int, matches *[]SearchMatch) (bool, error) {
	if remaining <= 0 {
		return true, nil
	}
	scanner := bufio.NewScanner(io.LimitReader(file, 8<<20))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		*matches = append(*matches, SearchMatch{
			Path: path, Line: lineNo, Column: loc[0] + 1, LineHash: lineHash(line), Preview: trimPreview(line, 500),
		})
		remaining--
		if remaining == 0 {
			return true, nil
		}
	}
	return false, scanner.Err()
}
