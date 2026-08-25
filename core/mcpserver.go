package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net/http"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func genMcpTool(tool LLMTool) (mcp.Tool, error) {
	schema, err := json.Marshal(tool.Schema)
	if err != nil {
		return mcp.Tool{}, fmt.Errorf("marshal schema for tool %q: %w", tool.Name, err)
	}
	return mcp.NewToolWithRawSchema(tool.Name, tool.Description, schema), nil
}

type llmToolSource interface {
	DefaultSystemPrompt(context.Context) (string, error)
	Tools(context.Context) ([]LLMTool, error)
	toolCallContext(context.Context) context.Context
}

// LLMToolServer exposes an MCP tool set independently of its transport.
type LLMToolServer struct {
	*mcpserver.MCPServer
	env         llmToolSource
	baseCtx     context.Context
	callContext func(context.Context, mcp.CallToolRequest) context.Context
	call        llmToolCallMiddleware
}

type llmToolCallHandler func(context.Context) (*mcp.CallToolResult, error)

// llmToolCallMiddleware wraps one complete MCP tool request. Harnesses use it
// to synchronize their mutable workspace before toolCallContext binds the
// Workspace argument and after both dispatch and tool-list refresh complete.
type llmToolCallMiddleware func(context.Context, llmToolCallHandler) (*mcp.CallToolResult, error)

// llmToolRequestContext takes cancellation and deadlines from the transport
// request while falling back to the server's construction context for Dagger
// session values. Streamable HTTP creates a fresh request context which does
// not otherwise carry client metadata, CurrentQuery, or the active schema.
type llmToolRequestContext struct {
	context.Context
	base context.Context
}

func (ctx llmToolRequestContext) Value(key any) any {
	if value := ctx.Context.Value(key); value != nil {
		return value
	}
	return ctx.base.Value(key)
}

// NewLLMToolServer initializes an MCP server from the tools in env.
func NewLLMToolServer(ctx context.Context, env *MCP) (*LLMToolServer, error) {
	return newLLMToolServer(ctx, env)
}

func newLLMToolServer(ctx context.Context, env llmToolSource) (*LLMToolServer, error) {
	instructions, err := env.DefaultSystemPrompt(ctx)
	if err != nil {
		return nil, fmt.Errorf("get MCP instructions: %w", err)
	}

	s := &LLMToolServer{
		MCPServer: mcpserver.NewMCPServer("Dagger", "0.0.1",
			mcpserver.WithInstructions(instructions)),
		env:     env,
		baseCtx: ctx,
	}
	if err := s.setTools(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *LLMToolServer) genMcpToolHandler(tool LLMTool) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// should never happen
		if request.Method != "tools/call" {
			return nil, fmt.Errorf("[dagger] expected MCP request method \"tools/call\" but received %q", request.Method)
		}
		ctx = llmToolRequestContext{Context: ctx, base: s.baseCtx}
		if s.callContext != nil {
			ctx = s.callContext(ctx, request)
		}

		call := func(ctx context.Context) (*mcp.CallToolResult, error) {
			ctx = s.env.toolCallContext(ctx)

			result, err := tool.Call(ctx, request.Params.Arguments)
			// TODO: differentiate user module's error from dagger error for better error message
			if err != nil {
				res := mcp.NewToolResultText(toolErrorMessage(err))
				res.IsError = true
				return res, nil
			}
			text, ok := result.(string)
			if !ok {
				b, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("[dagger] could not JSON marshal result %+v: %w", result, err)
				}
				text = string(b)
			}

			if err := s.setTools(ctx); err != nil {
				return nil, err
			}

			return mcp.NewToolResultText(text), nil
		}

		if s.call != nil {
			return s.call(ctx, call)
		}
		return call(ctx)
	}
}

// withCallContext lets a protocol bridge parent the authoritative Dagger tool
// evaluation beneath a vendor's live tool-call display span. It must be set
// before the server starts receiving requests.
func (s *LLMToolServer) withCallContext(callContext func(context.Context, mcp.CallToolRequest) context.Context) *LLMToolServer {
	s.callContext = callContext
	return s
}

// withCallMiddleware installs a transport-neutral request boundary. It must be
// configured before the server starts receiving requests.
func (s *LLMToolServer) withCallMiddleware(call llmToolCallMiddleware) *LLMToolServer {
	s.call = call
	return s
}

func (s *LLMToolServer) convertToMcpTools(llmTools []LLMTool) ([]mcpserver.ServerTool, error) {
	mcpTools := make([]mcpserver.ServerTool, 0, len(llmTools))
	for _, tool := range llmTools {
		// Skipping methods that return ID
		if strings.HasSuffix(tool.Name, "_id") {
			continue
		}

		mcpTool, err := genMcpTool(tool)
		if err != nil {
			return nil, err
		}
		mcpTools = append(mcpTools, mcpserver.ServerTool{Tool: mcpTool, Handler: s.genMcpToolHandler(tool)})
	}
	return mcpTools, nil
}

func (s *LLMToolServer) setTools(ctx context.Context) error {
	tools, err := s.env.Tools(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tools: %w", err)
	}
	mcpTools, err := s.convertToMcpTools(tools)
	if err != nil {
		return fmt.Errorf("failed to convert tools to MCP: %w", err)
	}
	s.SetTools(mcpTools...)
	return nil
}

// StreamableHTTPHandler returns a stateful Streamable HTTP MCP handler.
func (s *LLMToolServer) StreamableHTTPHandler() http.Handler {
	return mcpserver.NewStreamableHTTPServer(
		s.MCPServer,
		mcpserver.WithStateful(true),
	)
}

func (s *LLMToolServer) stdioServer(ctx context.Context) *mcpserver.StdioServer {
	stdioSrv := mcpserver.NewStdioServer(s.MCPServer)

	// MCP library requires standard log package.
	logger := stdlog.New(bklog.G(ctx).Writer(), "", 0)
	stdioSrv.SetErrorLogger(logger)
	return stdioSrv
}

// ServeStdio serves MCP over pipe until it closes or ctx is canceled.
func (s *LLMToolServer) ServeStdio(ctx context.Context, pipe io.ReadWriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error)
	stdioSrv := s.stdioServer(ctx)

	// Start MCP server in a goroutine.
	go func() {
		defer close(errCh)
		err := stdioSrv.Listen(ctx, pipe, pipe)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			select {
			case <-ctx.Done():
			case errCh <- fmt.Errorf("MCP server error: %w", err):
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (llm *LLM) MCP(ctx context.Context, _ *dagql.Server) error {
	// Under the object-tools scheme the LLM only acts through explicitly
	// bound objects. `dagger mcp` exposes the workspace: when nothing was
	// bound, bind each workspace module's main object so its methods are the
	// served MCP tools.
	if len(llm.mcp.boundTools) == 0 {
		bound, err := llm.mcp.bindWorkspaceModuleTools(ctx)
		if err != nil {
			return fmt.Errorf("bind workspace module tools: %w", err)
		}
		llm.mcp = bound
	}

	// Get engine client
	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current query: %w", err)
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("engine client error: %w", err)
	}

	rwc, err := bk.OpenPipe(ctx)
	if err != nil {
		return fmt.Errorf("open pipe error: %w", err)
	}

	s, err := NewLLMToolServer(ctx, llm.mcp)
	if err != nil {
		return err
	}
	return s.ServeStdio(ctx, rwc)
}
