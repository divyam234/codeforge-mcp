package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
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

type largeOutput struct {
	Data string `json:"data"`
}

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

func TestOpenAPIOperationsAreNotConsequential(t *testing.T) {
	doc := testServer().OpenAPI()
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatalf("OpenAPI paths missing: %#v", doc["paths"])
	}
	for path, pathItem := range paths {
		operations, ok := pathItem.(map[string]any)
		if !ok {
			t.Fatalf("path item %s is not an object: %#v", path, pathItem)
		}
		for method, operation := range operations {
			op, ok := operation.(map[string]any)
			if !ok {
				t.Fatalf("operation %s %s is not an object: %#v", method, path, operation)
			}
			if value, ok := op["x-openai-isConsequential"].(bool); !ok || value {
				t.Fatalf("operation %s %s missing x-openai-isConsequential=false: %#v", method, path, op["x-openai-isConsequential"])
			}
		}
	}
}

func TestOpenAPIHandlerReturnsJSON(t *testing.T) {
	server := testServer()
	req := httptest.NewRequest(http.MethodGet, "http://codeforge.local/openapi.json", nil)
	rec := httptest.NewRecorder()
	server.handleOpenAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode OpenAPI JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("unexpected OpenAPI version: %#v", doc["openapi"])
	}
	servers := doc["servers"].([]any)
	serverInfo := servers[0].(map[string]any)
	if serverInfo["url"] != "http://codeforge.local" {
		t.Fatalf("unexpected server URL: %#v", serverInfo["url"])
	}
}

func TestOpenAPIHandlerUsesForwardedServerURL(t *testing.T) {
	server := testServer()
	req := httptest.NewRequest(http.MethodGet, "http://internal:8080/openapi.json", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "example.com")
	rec := httptest.NewRecorder()
	server.handleOpenAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode OpenAPI JSON: %v", err)
	}
	servers := doc["servers"].([]any)
	serverInfo := servers[0].(map[string]any)
	if serverInfo["url"] != "https://example.com" {
		t.Fatalf("unexpected server URL: %#v", serverInfo["url"])
	}
}

func TestRESTToolCallInvokesRegisteredHandler(t *testing.T) {
	server := testServer()
	req := httptest.NewRequest(http.MethodPost, "/tools/hello", strings.NewReader(`{"name":"REST"}`))
	rec := httptest.NewRecorder()
	server.handleToolCall(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var output helloOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.Message != "hello REST" {
		t.Fatalf("unexpected output: %#v", output)
	}
}

func TestHTTPHandlerAllowsRequestsWhenAPIKeyIsUnset(t *testing.T) {
	server := testServer()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHandlerRequiresAPIKeyWhenConfigured(t *testing.T) {
	server := testServer()
	server.SetAPIKey("secret")

	req := httptest.NewRequest(http.MethodPost, "/tools/hello", strings.NewReader(`{"name":"REST"}`))
	rec := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without key = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/tools/hello", strings.NewReader(`{"name":"REST"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with bearer key = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/tools/hello", strings.NewReader(`{"name":"REST"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with x-api-key = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPIEndpointStaysPublicWhenAPIKeyConfigured(t *testing.T) {
	server := testServer()
	server.SetAPIKey("secret")
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPIIncludesSecurityWhenAPIKeyConfigured(t *testing.T) {
	server := testServer()
	if _, ok := server.OpenAPI()["security"]; ok {
		t.Fatal("security should be absent when API key is unset")
	}
	server.SetAPIKey("secret")
	if _, ok := server.OpenAPI()["security"]; !ok {
		t.Fatal("security missing when API key is configured")
	}
}

func TestRESTToolCallRejectsOversizedRequest(t *testing.T) {
	server := testServer()
	req := httptest.NewRequest(http.MethodPost, "/tools/hello", strings.NewReader(strings.Repeat("x", maxActionPayloadBytes+1)))
	rec := httptest.NewRecorder()
	server.handleToolCall(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTToolCallRejectsOversizedResponse(t *testing.T) {
	server := NewServer("test", "1", "instructions", slog.New(slog.NewTextHandler(io.Discard, nil)))
	AddTool(server, ToolSpec{Name: "large", Title: "Large", Description: "large"}, func(context.Context, emptyInput) (largeOutput, error) {
		return largeOutput{Data: strings.Repeat("x", maxActionPayloadBytes)}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/tools/large", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.handleToolCall(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPITrimsDescriptionsToActionLimits(t *testing.T) {
	server := NewServer("test", "1", strings.Repeat("i", maxOpenAPIDescription+1), slog.New(slog.NewTextHandler(io.Discard, nil)))
	AddTool(server, ToolSpec{Name: "long", Title: strings.Repeat("t", maxOpenAPIDescription+1), Description: strings.Repeat("d", maxOpenAPIDescription+1)}, func(context.Context, helloInput) (helloOutput, error) {
		return helloOutput{}, nil
	})
	doc := server.OpenAPI()
	info := doc["info"].(map[string]any)
	if len(info["description"].(string)) > maxOpenAPIDescription {
		t.Fatal("info description was not trimmed")
	}
	paths := doc["paths"].(map[string]any)
	operation := paths["/tools/long"].(map[string]any)["post"].(map[string]any)
	if len(operation["summary"].(string)) > maxOpenAPIDescription || len(operation["description"].(string)) > maxOpenAPIDescription {
		t.Fatalf("operation descriptions were not trimmed: %#v", operation)
	}
}

func TestSchemaDescriptionsTrimToActionLimits(t *testing.T) {
	schema := &jsonschema.Schema{Description: strings.Repeat("s", maxSchemaDescription+1)}
	trimSchemaDescriptions(schema)
	if len(schema.Description) > maxSchemaDescription {
		t.Fatal("schema description was not trimmed")
	}
}
