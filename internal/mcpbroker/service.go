// Package mcpbroker terminates local MCP sessions and exposes only the exact
// tool grant parsed from Torio's root-owned policy documents (ADR-0012).
package mcpbroker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wzslr321/torio/internal/lima"
)

// Upstream is the part of an initialized MCP client session the policy proxy
// uses. The official SDK's ClientSession implements it directly.
type Upstream interface {
	ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

// Recorder persists one policy decision before an allowed call crosses the
// custody boundary. A failed record is a denial, never a best-effort warning.
type Recorder interface {
	Record(AuditRecord) error
}

// ServiceConfig binds one local MCP server to one service policy and one
// already-authorized upstream session. PeerUID comes from SO_PEERCRED for the
// accepted Unix connection; it is never supplied by the MCP client.
type ServiceConfig struct {
	Service  string
	Policy   lima.Set
	Upstream Upstream
	Audit    Recorder
	PeerUID  uint32
	// CallTimeout bounds one external tool invocation. Zero selects 60 seconds.
	CallTimeout time.Duration
}

const defaultCallTimeout = 60 * time.Second

// NewServiceServer builds an MCP server whose advertised tool set is the exact
// intersection required by policy. Every policy tool must exist upstream; a
// partial intersection is refused because it is not the grant the operator
// wrote and not the generation status reports.
func NewServiceServer(ctx context.Context, cfg ServiceConfig) (*mcp.Server, error) {
	if err := lima.ValidateServiceName(cfg.Service); err != nil {
		return nil, err
	}
	if cfg.Upstream == nil {
		return nil, errors.New("MCP service needs an initialized upstream")
	}
	if cfg.Audit == nil {
		return nil, errors.New("MCP service needs an audit recorder")
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}

	grant, ok := serviceGrant(cfg.Policy, cfg.Service)
	if !ok {
		return nil, fmt.Errorf("policy holds no service %q", cfg.Service)
	}
	available, err := listAllTools(ctx, cfg.Upstream)
	if err != nil {
		return nil, errors.New("could not enumerate upstream MCP tools")
	}

	byName := make(map[string]*mcp.Tool, len(available))
	for _, tool := range available {
		if tool == nil || tool.Name == "" {
			return nil, errors.New("upstream returned an unnamed MCP tool")
		}
		if _, duplicate := byName[tool.Name]; duplicate {
			return nil, errors.New("upstream returned duplicate MCP tool names")
		}
		byName[tool.Name] = tool
	}

	writes := make(map[string]bool, len(grant.Tools))
	for _, allowed := range grant.Tools {
		if _, exists := byName[allowed.Name]; !exists {
			return nil, fmt.Errorf("policy tool %q is absent upstream", allowed.Name)
		}
		writes[allowed.Name] = allowed.Writes
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "torio-mcp-broker-" + cfg.Service, Version: "1"},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{},
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	)
	server.AddReceivingMiddleware(auditMiddleware(cfg, writes))

	// Stable registration order makes tools/list byte-stable even if an
	// upstream paginates or reorders its own response.
	names := make([]string, 0, len(writes))
	for name := range writes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tool := *byName[name]
		server.AddTool(&tool, forwardTool(cfg.Upstream, cfg.CallTimeout))
	}
	return server, nil
}

func serviceGrant(set lima.Set, name string) (lima.ServiceGrant, bool) {
	for _, grant := range set.Grants().Services {
		if grant.Name == name {
			return grant, true
		}
	}
	return lima.ServiceGrant{}, false
}

func listAllTools(ctx context.Context, upstream Upstream) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	cursor := ""
	for {
		result, err := upstream.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, errors.New("upstream returned no tools result")
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		if result.NextCursor == cursor {
			return nil, errors.New("upstream repeated its tools cursor")
		}
		cursor = result.NextCursor
	}
}

func forwardTool(upstream Upstream, timeout time.Duration) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, err := upstream.CallTool(callCtx, &mcp.CallToolParams{
			Meta:           request.Params.Meta,
			Name:           request.Params.Name,
			Arguments:      request.Params.Arguments,
			InputResponses: request.Params.InputResponses,
			RequestState:   request.Params.RequestState,
		})
		if err != nil {
			// SDK and provider errors may include response content. Neither crosses
			// into a Torio diagnostic or the local protocol error.
			return nil, errors.New("upstream MCP tool call failed")
		}
		if result == nil {
			return nil, errors.New("upstream MCP tool call returned no result")
		}
		return result, nil
	}
}

func auditMiddleware(cfg ServiceConfig, writes map[string]bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}
			params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || params == nil {
				return nil, errors.New("invalid MCP tool call")
			}
			isWrite, allowed := writes[params.Name]
			if err := cfg.Audit.Record(newAuditRecord(cfg.Service, params.Name, cfg.PeerUID, isWrite, allowed)); err != nil {
				return nil, errors.New("MCP call refused because its audit record could not be written")
			}
			if !allowed {
				return nil, errors.New("MCP tool is not granted by policy")
			}
			return next(ctx, method, request)
		}
	}
}
