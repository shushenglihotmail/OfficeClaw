// Package mcp implements the Model Context Protocol (MCP) server.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/officeclaw/src/tools"
)

const (
	ProtocolVersion = "2024-11-05"
	ServerName      = "officeclaw"
	ServerVersion   = "1.0.0"
)

// Server implements the MCP server over stdio.
type Server struct {
	toolRegistry *tools.Registry
	logger       *log.Logger
	initialized  bool
}

// NewServer creates a new MCP server.
func NewServer(toolRegistry *tools.Registry, logger *log.Logger) *Server {
	return &Server{
		toolRegistry: toolRegistry,
		logger:       logger,
	}
}

// Run starts the MCP server, reading from stdin and writing to stdout.
// Logs are written to stderr to avoid interfering with the MCP protocol.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Println("[mcp] Server starting...")

	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read line (JSON-RPC messages are newline-delimited)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				s.logger.Println("[mcp] Client disconnected (EOF)")
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		// Parse request
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(writer, NewErrorResponse(nil, ErrParseError, "Parse error"))
			continue
		}

		// Handle request
		resp := s.handleRequest(ctx, req)

		// Write response (only if ID is present - notifications don't get responses)
		if req.ID != nil {
			s.writeResponse(writer, resp)
		}
	}
}

// writeResponse writes a JSON-RPC response to the writer.
func (s *Server) writeResponse(w io.Writer, resp JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Printf("[mcp] Failed to marshal response: %v", err)
		return
	}
	data = append(data, '\n')
	w.Write(data)
}

// handleRequest routes the request to the appropriate handler.
func (s *Server) handleRequest(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	s.logger.Printf("[mcp] Received: method=%s", req.Method)

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		// Notification - no response needed
		s.logger.Println("[mcp] Client initialized")
		return JSONRPCResponse{} // Empty, won't be sent
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "ping":
		return NewSuccessResponse(req.ID, map[string]interface{}{})
	default:
		return NewErrorResponse(req.ID, ErrMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

// handleInitialize handles the initialize request.
func (s *Server) handleInitialize(req JSONRPCRequest) JSONRPCResponse {
	var params InitializeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return NewErrorResponse(req.ID, ErrInvalidParams, "Invalid params")
		}
	}

	s.logger.Printf("[mcp] Initialize from %s %s (protocol: %s)",
		params.ClientInfo.Name, params.ClientInfo.Version, params.ProtocolVersion)

	s.initialized = true

	return NewSuccessResponse(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapability{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
		},
		ServerInfo: ServerInfo{
			Name:    ServerName,
			Version: ServerVersion,
		},
	})
}

// handleToolsList handles the tools/list request.
func (s *Server) handleToolsList(req JSONRPCRequest) JSONRPCResponse {
	defs := s.toolRegistry.Definitions()

	mcpTools := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		mcpTools = append(mcpTools, ToolDefinition{
			Name:        def.Function.Name,
			Description: def.Function.Description,
			InputSchema: def.Function.Parameters,
		})
	}

	s.logger.Printf("[mcp] Listed %d tools", len(mcpTools))

	return NewSuccessResponse(req.ID, ToolsListResult{
		Tools: mcpTools,
	})
}

// handleToolsCall handles the tools/call request.
func (s *Server) handleToolsCall(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrInvalidParams, "Invalid params")
	}

	s.logger.Printf("[mcp] Tool call: %s", params.Name)

	// Get the tool
	tool, ok := s.toolRegistry.Get(params.Name)
	if !ok {
		return NewErrorResponse(req.ID, ErrInvalidParams, fmt.Sprintf("Unknown tool: %s", params.Name))
	}

	// Convert arguments to JSON string (tools expect JSON string)
	argsJSON, err := json.Marshal(params.Arguments)
	if err != nil {
		return NewErrorResponse(req.ID, ErrInvalidParams, "Failed to marshal arguments")
	}

	// Execute the tool
	result, err := tool.Execute(ctx, string(argsJSON))

	if err != nil {
		s.logger.Printf("[mcp] Tool error: %v", err)
		return NewSuccessResponse(req.ID, ToolsCallResult{
			Content: []ContentBlock{{
				Type: "text",
				Text: fmt.Sprintf("Error: %v", err),
			}},
			IsError: true,
		})
	}

	s.logger.Printf("[mcp] Tool result: %d chars", len(result))

	return NewSuccessResponse(req.ID, ToolsCallResult{
		Content: []ContentBlock{{
			Type: "text",
			Text: result,
		}},
		IsError: false,
	})
}
