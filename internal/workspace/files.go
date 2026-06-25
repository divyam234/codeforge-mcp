package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type ReadResult struct {
	Path         string `json:"path"`
	Snapshot     string `json:"snapshot"`
	Content      string `json:"content"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	TotalLines   int    `json:"total_lines"`
	Truncated    bool   `json:"truncated"`
	Newline      string `json:"newline"`
	FinalNewline bool   `json:"final_newline"`
	Mode         string `json:"mode"`
}

type WriteResult struct {
	Path        string `json:"path"`
	Snapshot    string `json:"snapshot"`
	Bytes       int    `json:"bytes"`
	Created     bool   `json:"created"`
	Overwritten bool   `json:"overwritten"`
}

type lineInfo struct {
	Text       string
	Start      int
	ContentEnd int
	End        int
	Ending     string
}

type document struct {
	data         []byte
	lines        []lineInfo
	newline      string
	finalNewline bool
	snapshot     string
}

func (w *Workspace) ReadFile(path string, startLine, endLine int, mode string) (ReadResult, error) {
	abs, err := w.Resolve(path, true)
	if err != nil {
		return ReadResult{}, err
	}
	doc, err := w.loadDocument(abs)
	if err != nil {
		return ReadResult{}, err
	}
	if mode == "" {
		mode = "hashline"
	}
	if mode != "hashline" && mode != "plain" {
		return ReadResult{}, errors.New("mode must be hashline or plain")
	}
	total := len(doc.lines)
	if total == 0 {
		return ReadResult{Path: filepath.ToSlash(path), Snapshot: doc.snapshot, TotalLines: 0, Newline: newlineName(doc.newline), FinalNewline: false, Mode: mode}, nil
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > total {
		endLine = total
	}
	if startLine > total || endLine < startLine {
		return ReadResult{}, fmt.Errorf("invalid line range %d..%d for %d-line file", startLine, endLine, total)
	}
	var out strings.Builder
	for i := startLine; i <= endLine; i++ {
		line := doc.lines[i-1]
		if mode == "hashline" {
			fmt.Fprintf(&out, "%d:%s|%s", i, lineHash(line.Text), line.Text)
		} else {
			fmt.Fprintf(&out, "%d\t%s", i, line.Text)
		}
		if i < endLine {
			out.WriteByte('\n')
		}
	}
	return ReadResult{
		Path:         filepath.ToSlash(path),
		Snapshot:     doc.snapshot,
		Content:      out.String(),
		StartLine:    startLine,
		EndLine:      endLine,
		TotalLines:   total,
		Truncated:    endLine < total,
		Newline:      newlineName(doc.newline),
		FinalNewline: doc.finalNewline,
		Mode:         mode,
	}, nil
}

func (w *Workspace) WriteFile(path, content, expectedSnapshot string, createOnly bool) (WriteResult, error) {
	if int64(len(content)) > w.MaxFileBytes {
		return WriteResult{}, errors.New("content exceeds maximum file size")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return WriteResult{}, errors.New("content must be UTF-8 text without NUL bytes")
	}
	abs, err := w.Resolve(path, false)
	if err != nil {
		return WriteResult{}, err
	}
	created := false
	mode := os.FileMode(0o644)
	current, statErr := os.Lstat(abs)
	switch {
	case statErr == nil:
		if !current.Mode().IsRegular() {
			return WriteResult{}, errors.New("destination is not a regular file")
		}
		if createOnly {
			return WriteResult{}, errors.New("file already exists")
		}
		mode = current.Mode().Perm()
		if expectedSnapshot != "" {
			doc, err := w.loadDocument(abs)
			if err != nil {
				return WriteResult{}, err
			}
			if !sameHash(expectedSnapshot, doc.snapshot) {
				return WriteResult{}, fmt.Errorf("stale file snapshot: expected %s, current %s", expectedSnapshot, doc.snapshot)
			}
		}
	case errors.Is(statErr, os.ErrNotExist):
		created = true
		if expectedSnapshot != "" {
			return WriteResult{}, errors.New("expected_snapshot was supplied but the file does not exist")
		}
	default:
		return WriteResult{}, statErr
	}
	if err := atomicWrite(abs, []byte(content), mode); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{
		Path:        filepath.ToSlash(path),
		Snapshot:    snapshot([]byte(content)),
		Bytes:       len(content),
		Created:     created,
		Overwritten: !created,
	}, nil
}

func (w *Workspace) loadDocument(abs string) (document, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return document{}, err
	}
	if !info.Mode().IsRegular() {
		return document{}, errors.New("path is not a regular file")
	}
	if info.Size() > w.MaxFileBytes {
		return document{}, fmt.Errorf("file exceeds maximum readable size of %d bytes", w.MaxFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return document{}, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return document{}, errors.New("binary or non-UTF-8 files are not supported")
	}
	return parseDocument(data), nil
}

func parseDocument(data []byte) document {
	lines := make([]lineInfo, 0, bytes.Count(data, []byte{'\n'})+1)
	lf, crlf := 0, 0
	for pos := 0; pos < len(data); {
		start := pos
		rel := bytes.IndexByte(data[pos:], '\n')
		if rel < 0 {
			lines = append(lines, lineInfo{Text: string(data[start:]), Start: start, ContentEnd: len(data), End: len(data)})
			pos = len(data)
			continue
		}
		idx := pos + rel
		contentEnd := idx
		ending := "\n"
		lf++
		if idx > start && data[idx-1] == '\r' {
			contentEnd--
			ending = "\r\n"
			crlf++
		}
		lines = append(lines, lineInfo{Text: string(data[start:contentEnd]), Start: start, ContentEnd: contentEnd, End: idx + 1, Ending: ending})
		pos = idx + 1
	}
	newline := "\n"
	if crlf > 0 && crlf*2 >= lf {
		newline = "\r\n"
	}
	finalNewline := len(lines) > 0 && lines[len(lines)-1].Ending != ""
	return document{data: data, lines: lines, newline: newline, finalNewline: finalNewline, snapshot: snapshot(data)}
}

func atomicWrite(path string, data []byte, mode os.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codeforge-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func snapshot(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func lineHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func sameHash(expected, actual string) bool {
	return strings.EqualFold(strings.TrimSpace(expected), actual)
}

func newlineName(value string) string {
	if value == "\r\n" {
		return "crlf"
	}
	return "lf"
}
