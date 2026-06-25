package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type HashEdit struct {
	Mode        string `json:"mode,omitempty"`
	StartLine   int    `json:"start_line"`
	StartHash   string `json:"start_hash"`
	EndLine     int    `json:"end_line,omitempty"`
	EndHash     string `json:"end_hash,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

type EditResult struct {
	Path        string     `json:"path"`
	OldSnapshot string     `json:"old_snapshot"`
	NewSnapshot string     `json:"new_snapshot"`
	Edits       int        `json:"edits"`
	BytesBefore int        `json:"bytes_before"`
	BytesAfter  int        `json:"bytes_after"`
	Preview     ReadResult `json:"preview"`
}

type resolvedEdit struct {
	start       int
	end         int
	replacement []byte
	line        int
	mode        string
}

func (w *Workspace) EditFile(path, expectedSnapshot string, edits []HashEdit) (EditResult, error) {
	if len(edits) == 0 {
		return EditResult{}, errors.New("at least one edit is required")
	}
	abs, err := w.Resolve(path, true)
	if err != nil {
		return EditResult{}, err
	}
	doc, err := w.loadDocument(abs)
	if err != nil {
		return EditResult{}, err
	}
	if expectedSnapshot == "" {
		return EditResult{}, errors.New("snapshot is required; call file_read first")
	}
	if !sameHash(expectedSnapshot, doc.snapshot) {
		return EditResult{}, fmt.Errorf("stale file snapshot: expected %s, current %s; read the file again", expectedSnapshot, doc.snapshot)
	}
	resolved := make([]resolvedEdit, 0, len(edits))
	for i, edit := range edits {
		r, err := resolveHashEdit(doc, edit)
		if err != nil {
			return EditResult{}, fmt.Errorf("edit %d: %w", i+1, err)
		}
		resolved = append(resolved, r)
	}
	if err := validateNonOverlapping(resolved); err != nil {
		return EditResult{}, err
	}
	sort.Slice(resolved, func(i, j int) bool {
		if resolved[i].start == resolved[j].start {
			return resolved[i].end > resolved[j].end
		}
		return resolved[i].start > resolved[j].start
	})
	updated := bytes.Clone(doc.data)
	firstLine := len(doc.lines)
	for _, edit := range resolved {
		updated = append(updated[:edit.start], append(edit.replacement, updated[edit.end:]...)...)
		if edit.line < firstLine {
			firstLine = edit.line
		}
	}
	if int64(len(updated)) > w.MaxFileBytes {
		return EditResult{}, errors.New("edited file exceeds maximum file size")
	}
	if !utf8.Valid(updated) || bytes.IndexByte(updated, 0) >= 0 {
		return EditResult{}, errors.New("edit would produce binary or non-UTF-8 content")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return EditResult{}, err
	}
	if err := atomicWrite(abs, updated, info.Mode().Perm()); err != nil {
		return EditResult{}, err
	}
	newDoc := parseDocument(updated)
	previewStart := max(1, firstLine-3)
	previewEnd := min(len(newDoc.lines), previewStart+11)
	preview, err := w.ReadFile(path, previewStart, previewEnd, "hashline")
	if err != nil {
		return EditResult{}, err
	}
	return EditResult{
		Path:        filepath.ToSlash(path),
		OldSnapshot: doc.snapshot,
		NewSnapshot: newDoc.snapshot,
		Edits:       len(edits),
		BytesBefore: len(doc.data),
		BytesAfter:  len(updated),
		Preview:     preview,
	}, nil
}

func resolveHashEdit(doc document, edit HashEdit) (resolvedEdit, error) {
	mode := strings.ToLower(strings.TrimSpace(edit.Mode))
	if mode == "" {
		mode = "replace"
	}
	if mode != "replace" && mode != "insert_before" && mode != "insert_after" {
		return resolvedEdit{}, errors.New("mode must be replace, insert_before, or insert_after")
	}
	if edit.StartLine < 1 || edit.StartLine > len(doc.lines) {
		return resolvedEdit{}, fmt.Errorf("start_line %d is outside 1..%d", edit.StartLine, len(doc.lines))
	}
	startLine := doc.lines[edit.StartLine-1]
	if edit.StartHash == "" || !sameHash(edit.StartHash, lineHash(startLine.Text)) {
		return resolvedEdit{}, fmt.Errorf("stale start anchor at line %d: expected hash %s, current hash %s", edit.StartLine, edit.StartHash, lineHash(startLine.Text))
	}
	replacement := normalizeNewlines(edit.Replacement, doc.newline)

	switch mode {
	case "insert_before":
		if replacement != "" && !endsWithNewline(replacement) {
			replacement += doc.newline
		}
		return resolvedEdit{start: startLine.Start, end: startLine.Start, replacement: []byte(replacement), line: edit.StartLine, mode: mode}, nil
	case "insert_after":
		if startLine.Ending == "" {
			if replacement != "" && !startsWithNewline(replacement) {
				replacement = doc.newline + replacement
			}
		} else if replacement != "" && !endsWithNewline(replacement) {
			replacement += doc.newline
		}
		return resolvedEdit{start: startLine.End, end: startLine.End, replacement: []byte(replacement), line: edit.StartLine + 1, mode: mode}, nil
	default:
		endLineNumber := edit.EndLine
		if endLineNumber == 0 {
			endLineNumber = edit.StartLine
		}
		if endLineNumber < edit.StartLine || endLineNumber > len(doc.lines) {
			return resolvedEdit{}, fmt.Errorf("end_line %d is outside %d..%d", endLineNumber, edit.StartLine, len(doc.lines))
		}
		endLine := doc.lines[endLineNumber-1]
		if edit.EndHash == "" {
			if endLineNumber == edit.StartLine {
				edit.EndHash = edit.StartHash
			} else {
				return resolvedEdit{}, errors.New("end_hash is required for a multi-line replacement")
			}
		}
		if !sameHash(edit.EndHash, lineHash(endLine.Text)) {
			return resolvedEdit{}, fmt.Errorf("stale end anchor at line %d: expected hash %s, current hash %s", endLineNumber, edit.EndHash, lineHash(endLine.Text))
		}
		if endLine.Ending != "" && replacement != "" && !endsWithNewline(replacement) {
			replacement += doc.newline
		}
		return resolvedEdit{start: startLine.Start, end: endLine.End, replacement: []byte(replacement), line: edit.StartLine, mode: mode}, nil
	}
}

func validateNonOverlapping(edits []resolvedEdit) error {
	ordered := append([]resolvedEdit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].start == ordered[j].start {
			return ordered[i].end < ordered[j].end
		}
		return ordered[i].start < ordered[j].start
	})
	for i := 1; i < len(ordered); i++ {
		prev, current := ordered[i-1], ordered[i]
		if current.start < prev.end || (current.start == prev.start && (current.start == current.end || prev.start == prev.end)) {
			return errors.New("edits overlap or target the same insertion point")
		}
	}
	return nil
}

func normalizeNewlines(value, newline string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if newline == "\r\n" {
		value = strings.ReplaceAll(value, "\n", "\r\n")
	}
	return value
}

func endsWithNewline(value string) bool {
	return strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\r")
}

func startsWithNewline(value string) bool {
	return strings.HasPrefix(value, "\n") || strings.HasPrefix(value, "\r")
}
