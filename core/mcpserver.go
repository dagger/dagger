package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
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

type mcpServer struct {
	*mcpserver.MCPServer
	dag  *dagql.Server
	env  *MCP
	pipe io.ReadWriteCloser
}

func (s mcpServer) genMcpToolHandler(tool LLMTool) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// should never happen
		if request.Method != "tools/call" {
			return nil, fmt.Errorf("[dagger] expected MCP request method \"tools/call\" but received %q", request.Method)
		}

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
}

func (s mcpServer) convertToMcpTools(llmTools []LLMTool) ([]mcpserver.ServerTool, error) {
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

func (s mcpServer) setTools(ctx context.Context) error {
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

func (s mcpServer) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := s.setTools(ctx); err != nil {
		return err
	}

	errCh := make(chan error)

	stdioSrv := mcpserver.NewStdioServer(s.MCPServer)

	// MCP library requires standard log package
	logger := stdlog.New(bklog.G(ctx).Writer(), "", 0)
	stdioSrv.SetErrorLogger(logger)

	// Start MCP server in a goroutine
	go func() {
		defer close(errCh)
		err := stdioSrv.Listen(ctx, s.pipe, s.pipe)
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

func (llm *LLM) MCP(ctx context.Context, dag *dagql.Server) error {
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

	instructions, err := llm.mcp.DefaultSystemPrompt(ctx)
	if err != nil {
		return err
	}

	s := mcpServer{
		mcpserver.NewMCPServer("Dagger", "0.0.1",
			mcpserver.WithInstructions(instructions)),
		dag,
		llm.mcp.Standalone(),
		rwc,
	}

	return s.run(ctx)
}
