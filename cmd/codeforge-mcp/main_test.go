package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestVersionRequested(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		if !versionRequested(args) {
			t.Fatalf("versionRequested(%q) = false", args)
		}
	}
	for _, args := range [][]string{nil, {}, {"--help"}, {"--version", "extra"}} {
		if versionRequested(args) {
			t.Fatalf("versionRequested(%q) = true", args)
		}
	}
}

func TestLoggerCanBeDirectedAwayFromStdout(t *testing.T) {
	var stderr bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&stderr, nil))
	logger.Info("stdio-safe")
	if !strings.Contains(stderr.String(), "stdio-safe") {
		t.Fatalf("logger did not write to the configured stderr sink: %q", stderr.String())
	}
}

// TestCompiledStdioProtocol launches this test binary as a real CodeForge
// subprocess and connects through the official SDK's CommandTransport. This
// exercises the same os.Stdin/os.Stdout path used by the production binary. If
// any log text leaks to stdout, the SDK cannot decode the JSON-RPC stream and
// this test fails during initialization.
func TestCompiledStdioProtocol(t *testing.T) {
	if os.Getenv("CODEFORGE_STDIO_HELPER") == "1" {
		t.Fatal("stdio parent test unexpectedly running inside helper")
	}

	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodeForgeStdioHelperProcess$", "-test.v=false")
	command.Env = filteredEnvironment(os.Environ(), "CODEFORGE_")
	command.Env = append(command.Env,
		"CODEFORGE_STDIO_HELPER=1",
		"CODEFORGE_WORKSPACE_ROOT="+root,
		"CODEFORGE_COMMAND_POLICY=unrestricted",
		"CODEFORGE_STATE_DIR="+root+"/state",
	)
	var stderr lockedBuffer
	command.Stderr = &stderr

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "codeforge-stdio-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.CommandTransport{
		Command:           command,
		TerminateDuration: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("connect to stdio subprocess: %v\nstderr:\n%s", err, stderr.String())
	}
	defer func() {
		if err := session.Close(); err != nil && !strings.Contains(err.Error(), "signal: terminated") {
			t.Errorf("close stdio session: %v\nstderr:\n%s", err, stderr.String())
		}
	}()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools over stdio: %v\nstderr:\n%s", err, stderr.String())
	}
	if got, want := len(listed.Tools), 32; got != want {
		t.Fatalf("stdio server advertised %d tools, want %d", got, want)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "workspace_info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call workspace_info over stdio: %v\nstderr:\n%s", err, stderr.String())
	}
	if result.IsError {
		t.Fatalf("workspace_info returned tool error: %#v", result)
	}
	if !strings.Contains(stderr.String(), `"transport":"stdio"`) {
		t.Fatalf("expected startup log on stderr, got: %q", stderr.String())
	}
}

func TestCodeForgeStdioHelperProcess(t *testing.T) {
	if os.Getenv("CODEFORGE_STDIO_HELPER") != "1" {
		return
	}
	os.Exit(run())
}

func filteredEnvironment(environment []string, prefixes ...string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			result = append(result, entry)
		}
	}
	return result
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Len()
}
