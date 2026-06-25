package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type CommandResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func (w *Workspace) Git(args ...string) (CommandResult, error) {
	return w.run(30*time.Second, "git", args...)
}

func (w *Workspace) ApplyPatch(patch string, checkOnly bool) (CommandResult, error) {
	if strings.TrimSpace(patch) == "" {
		return CommandResult{}, errors.New("patch is required")
	}
	if int64(len(patch)) > w.MaxFileBytes*4 {
		return CommandResult{}, errors.New("patch is too large")
	}
	if err := validatePatchPaths(w, patch); err != nil {
		return CommandResult{}, err
	}
	args := []string{"apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = w.Root
	cmd.Stdin = strings.NewReader(patch)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	result := CommandResult{Output: combined.String(), ExitCode: exitCode(err)}
	if err != nil {
		return result, fmt.Errorf("git apply failed: %w", err)
	}
	return result, nil
}

func (w *Workspace) run(timeout time.Duration, name string, args ...string) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = w.Root
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	result := CommandResult{Output: combined.String(), ExitCode: exitCode(err)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func validatePatchPaths(w *Workspace, patch string) error {
	if strings.ContainsRune(patch, '\x00') {
		return errors.New("patch contains NUL")
	}
	pathCount := 0
	for _, line := range strings.Split(patch, "\n") {
		var raw string
		switch {
		case strings.HasPrefix(line, "+++ "):
			raw = strings.TrimPrefix(line, "+++ ")
		case strings.HasPrefix(line, "--- "):
			raw = strings.TrimPrefix(line, "--- ")
		case strings.HasPrefix(line, "rename from "):
			raw = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			raw = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "copy from "):
			raw = strings.TrimPrefix(line, "copy from ")
		case strings.HasPrefix(line, "copy to "):
			raw = strings.TrimPrefix(line, "copy to ")
		default:
			continue
		}
		path, err := parsePatchPath(raw)
		if err != nil {
			return err
		}
		if path == "/dev/null" {
			continue
		}
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "b/")
		if path == "" {
			return errors.New("patch contains an empty path")
		}
		if _, err := w.Resolve(path, false); err != nil {
			return fmt.Errorf("unsafe patch path %q: %w", path, err)
		}
		pathCount++
	}
	if pathCount == 0 {
		return errors.New("patch contains no file paths")
	}
	return nil
}

func parsePatchPath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.Split(raw, "\t")[0])
	if strings.HasPrefix(raw, `"`) {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid quoted patch path %q: %w", raw, err)
		}
		return value, nil
	}
	return raw, nil
}
