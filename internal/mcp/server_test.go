package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type helloInput struct {
	Name string `json:"name" jsonschema:"name to greet"`
}

type helloOutput struct {
	Message string `json:"message" jsonschema:"generated greeting"`
}

type emptyInput struct{}
type emptyOutput struct{}

func testServer() *Server {
	server := NewServer("test", "1", "instructions", slog.New(slog.NewTextHandler(io.Discard, nil)))
	AddTool(server, ToolSpec{Name: "hello", Title: "Hello", Description: "hello", ReadOnly: true, Idempotent: true}, func(_ context.Context, in helloInput) (helloOutput, error) {
		return helloOutput{Message: "hello " + in.Name}, nil
	})
	AddTool(server, ToolSpec{Name: "fail", Description: "fail"}, func(context.Context, emptyInput) (emptyOutput, error) {
		return emptyOutput{}, errors.New("tool failed")
	})
	return server
}

func connectTestClient(t *testing.T, server *Server) *sdkmcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.RunTransport(ctx, serverTransport) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case err := <-serverErrors:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	})
	return session
}

func TestTypedToolExposesInputAndOutputSchemas(t *testing.T) {
	session := connectTestClient(t, testServer())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var hello *sdkmcp.Tool
	for _, tool := range listed.Tools {
		if tool.Name == "hello" {
			hello = tool
		}
	}
	if hello == nil || hello.InputSchema == nil || hello.OutputSchema == nil {
		t.Fatalf("typed schemas missing: %#v", hello)
	}
	if hello.Annotations == nil || !hello.Annotations.ReadOnlyHint {
		t.Fatalf("annotations missing: %#v", hello)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "hello", Arguments: map[string]any{"name": "world"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %#v", result)
	}
	content, ok := result.StructuredContent.(map[string]any)
	if !ok || content["message"] != "hello world" {
		t.Fatalf("unexpected structured result: %#v", result.StructuredContent)
	}
}

func TestTypedToolErrorIsVisibleToModel(t *testing.T) {
	session := connectTestClient(t, testServer())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "fail", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("tool failure became protocol failure: %v", err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("unexpected error result: %#v", result)
	}
}

func TestDuplicateToolPanics(t *testing.T) {
	server := NewServer("test", "1", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	spec := ToolSpec{Name: "same"}
	AddTool(server, spec, func(context.Context, emptyInput) (emptyOutput, error) { return emptyOutput{}, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate tool panic")
		}
	}()
	AddTool(server, spec, func(context.Context, emptyInput) (emptyOutput, error) { return emptyOutput{}, nil })
}
