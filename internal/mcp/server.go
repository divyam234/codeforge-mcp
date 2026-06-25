package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	actionTimeout         = 44 * time.Second
	maxActionPayloadBytes = 99_999
	maxOpenAPIDescription = 300
	maxSchemaDescription  = 700
)

type Server struct {
	sdk          *sdkmcp.Server
	name         string
	version      string
	instructions string
	apiKey       string

	mu    sync.RWMutex
	tools map[string]toolEntry
}

func (s *Server) SetAPIKey(apiKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKey = strings.TrimSpace(apiKey)
}

type toolEntry struct {
	spec         ToolSpec
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
	call         func(context.Context, json.RawMessage) (any, error)
}

type ToolSpec struct {
	Name        string
	Title       string
	Description string
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

func NewServer(name, version, instructions string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		name:         name,
		version:      version,
		instructions: instructions,
		sdk: sdkmcp.NewServer(
			&sdkmcp.Implementation{Name: name, Version: version},
			&sdkmcp.ServerOptions{Instructions: instructions, Logger: logger},
		),
		tools: make(map[string]toolEntry),
	}
}

func AddTool[In, Out any](s *Server, spec ToolSpec, handler func(context.Context, In) (Out, error)) {
	if spec.Name == "" {
		panic("empty MCP tool name")
	}
	if handler == nil {
		panic("nil MCP tool handler: " + spec.Name)
	}
	s.mu.RLock()
	_, exists := s.tools[spec.Name]
	s.mu.RUnlock()
	if exists {
		panic("duplicate MCP tool: " + spec.Name)
	}

	inputSchema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Errorf("MCP tool %q input schema: %w", spec.Name, err))
	}
	outputSchema, err := jsonschema.For[Out](nil)
	if err != nil {
		panic(fmt.Errorf("MCP tool %q output schema: %w", spec.Name, err))
	}

	openWorld := spec.OpenWorld
	destructive := spec.Destructive
	tool := &sdkmcp.Tool{
		Name:        spec.Name,
		Title:       spec.Title,
		Description: spec.Description,
		Annotations: &sdkmcp.ToolAnnotations{
			Title:           spec.Title,
			ReadOnlyHint:    spec.ReadOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  spec.Idempotent,
			OpenWorldHint:   &openWorld,
		},
	}

	sdkmcp.AddTool(s.sdk, tool, func(ctx context.Context, _ *sdkmcp.CallToolRequest, input In) (*sdkmcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		return nil, output, err
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[spec.Name]; exists {
		panic("duplicate MCP tool: " + spec.Name)
	}
	s.tools[spec.Name] = toolEntry{
		spec:         spec,
		inputSchema:  inputSchema,
		outputSchema: outputSchema,
		call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input In
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &input); err != nil {
					return nil, fmt.Errorf("decode request body: %w", err)
				}
			}
			return handler(ctx, input)
		},
	}
}

func (s *Server) Run(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdkmcp.StdioTransport{})
}

func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.HTTPHandler()}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	return srv.ListenAndServe()
}

func (s *Server) HTTPHandler() http.Handler {
	mcpHandler := sdkmcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *sdkmcp.Server { return s.sdk },
		nil,
	)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	mux.HandleFunc("/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/tools/", s.handleToolCall)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "CodeForge MCP\n\nMCP: /mcp\nOpenAPI: /openapi.json\nREST tools: /tools/{tool_name}\n")
			return
		}
		http.NotFound(w, r)
	})
	return s.requireAPIKey(mux)
}

func (s *Server) RunTransport(ctx context.Context, transport sdkmcp.Transport) error {
	return s.sdk.Run(ctx, transport)
}

func (s *Server) SDK() *sdkmcp.Server { return s.sdk }

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		apiKey := s.apiKey
		s.mu.RUnlock()
		if apiKey == "" || r.URL.Path == "/openapi.json" || validAPIKey(r, apiKey) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="codeforge"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func validAPIKey(r *http.Request, apiKey string) bool {
	provided := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if provided == "" {
		provided = bearerToken(r.Header.Get("Authorization"))
	}
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(apiKey)) == 1
}

func bearerToken(header string) string {
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.OpenAPIForRequest(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/tools/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	s.mu.RLock()
	entry, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxActionPayloadBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), actionTimeout)
	defer cancel()
	result := make(chan toolCallResult, 1)
	go func() {
		output, err := entry.call(ctx, body)
		result <- toolCallResult{output: output, err: err}
	}()

	var call toolCallResult
	select {
	case call = <-result:
	case <-ctx.Done():
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "tool call timed out"})
		return
	}
	output, err := call.output, call.err
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeBoundedJSON(w, http.StatusOK, output)
}

type toolCallResult struct {
	output any
	err    error
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeBoundedJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if len(data) > maxActionPayloadBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "tool response exceeds OpenAPI action size limit"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func (s *Server) OpenAPI() map[string]any {
	return s.openAPI("")
}

func (s *Server) OpenAPIForRequest(r *http.Request) map[string]any {
	return s.openAPI(requestBaseURL(r))
}

func (s *Server) openAPI(serverURL string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	paths := make(map[string]any, len(s.tools))
	for name, entry := range s.tools {
		inputSchema := cloneSchema(entry.inputSchema)
		trimSchemaDescriptions(inputSchema)
		outputSchema := cloneSchema(entry.outputSchema)
		trimSchemaDescriptions(outputSchema)
		operation := map[string]any{
			"operationId":              name,
			"summary":                  truncateString(entry.spec.Title, maxOpenAPIDescription),
			"description":              truncateString(entry.spec.Description, maxOpenAPIDescription),
			"x-openai-isConsequential": false,
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{"schema": inputSchema},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Tool result",
					"content": map[string]any{
						"application/json": map[string]any{"schema": outputSchema},
					},
				},
				"400": map[string]any{"description": "Tool error or invalid request"},
			},
		}
		paths["/tools/"+name] = map[string]any{"post": operation}
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       s.name,
			"version":     s.version,
			"description": truncateString(s.instructions, maxOpenAPIDescription),
		},
		"paths": paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "API key",
				},
			},
		},
	}
	if s.apiKey != "" {
		doc["security"] = []map[string][]string{{"bearerAuth": []string{}}}
	}
	if serverURL != "" {
		doc["servers"] = []map[string]string{{"url": serverURL}}
	}
	return doc
}

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	proto := firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		proto = forwardedProto(r.Header.Get("Forwarded"))
	}
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + host
}

func firstHeaderValue(value string) string {
	value, _, _ = strings.Cut(value, ",")
	return strings.TrimSpace(value)
}

func forwardedProto(header string) string {
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(key, "proto") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

func cloneSchema(schema *jsonschema.Schema) *jsonschema.Schema {
	if schema == nil {
		return nil
	}
	return schema.CloneSchemas()
}

func trimSchemaDescriptions(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	schema.Description = truncateString(schema.Description, maxSchemaDescription)
	for _, child := range schema.Defs {
		trimSchemaDescriptions(child)
	}
	for _, child := range schema.Definitions {
		trimSchemaDescriptions(child)
	}
	for _, child := range schema.Properties {
		trimSchemaDescriptions(child)
	}
	for _, child := range schema.PatternProperties {
		trimSchemaDescriptions(child)
	}
	trimSchemaDescriptions(schema.AdditionalProperties)
	trimSchemaDescriptions(schema.PropertyNames)
	trimSchemaDescriptions(schema.UnevaluatedProperties)
	trimSchemaDescriptions(schema.Items)
	for _, child := range schema.PrefixItems {
		trimSchemaDescriptions(child)
	}
	trimSchemaDescriptions(schema.AdditionalItems)
	trimSchemaDescriptions(schema.Contains)
	trimSchemaDescriptions(schema.UnevaluatedItems)
	for _, child := range schema.AllOf {
		trimSchemaDescriptions(child)
	}
	for _, child := range schema.AnyOf {
		trimSchemaDescriptions(child)
	}
	for _, child := range schema.OneOf {
		trimSchemaDescriptions(child)
	}
	trimSchemaDescriptions(schema.Not)
	trimSchemaDescriptions(schema.If)
	trimSchemaDescriptions(schema.Then)
	trimSchemaDescriptions(schema.Else)
	for _, child := range schema.DependentSchemas {
		trimSchemaDescriptions(child)
	}
	trimSchemaDescriptions(schema.ContentSchema)
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
