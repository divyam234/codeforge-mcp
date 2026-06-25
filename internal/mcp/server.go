package mcp

import (
	"context"
	"log/slog"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	sdk *sdkmcp.Server

	mu    sync.Mutex
	tools map[string]struct{}
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
		sdk: sdkmcp.NewServer(
			&sdkmcp.Implementation{Name: name, Version: version},
			&sdkmcp.ServerOptions{Instructions: instructions, Logger: logger},
		),
		tools: make(map[string]struct{}),
	}
}

func AddTool[In, Out any](s *Server, spec ToolSpec, handler func(context.Context, In) (Out, error)) {
	if spec.Name == "" {
		panic("empty MCP tool name")
	}
	if handler == nil {
		panic("nil MCP tool handler: " + spec.Name)
	}

	s.mu.Lock()
	if _, exists := s.tools[spec.Name]; exists {
		s.mu.Unlock()
		panic("duplicate MCP tool: " + spec.Name)
	}
	s.tools[spec.Name] = struct{}{}
	s.mu.Unlock()

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
}

func (s *Server) Run(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdkmcp.StdioTransport{})
}

func (s *Server) RunTransport(ctx context.Context, transport sdkmcp.Transport) error {
	return s.sdk.Run(ctx, transport)
}

func (s *Server) SDK() *sdkmcp.Server { return s.sdk }
