package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/moby/buildkit/util/bklog"
)

// mcpDefaultAny lets us skip the typed defaults
func mcpDefaultAny(v any) mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["default"] = v
	}
}

func genMcpToolOpts(tool LLMTool) ([]mcp.ToolOption, error) {
	println("  🍎 genMcpToolOpts " + tool.Name)
	defer println("  🍎 genMcpToolOpts " + tool.Name + " returned")
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription(tool.Description),
	}
	var required []string
	if v, ok := tool.Schema["required"]; ok {
		required, ok = v.([]string)
		if !ok {
			return nil, fmt.Errorf("expecting type []string for \"required\" for tool %q", tool.Name)
		}
	}
	props, ok := tool.Schema["properties"]
	if !ok {
		return nil, fmt.Errorf("schema of tool %q is missing \"properties\": %+v", tool.Name, tool.Schema)
	}
	for argName, v := range props.(map[string]any) {
		var propOpts []mcp.PropertyOption
		argSchema := v.(map[string]any)
		if desc, ok := argSchema["description"]; ok {
			s, ok := desc.(string)
			if !ok {
				return nil, fmt.Errorf("description of arg %q of tool %q is expected to be of type string, but is %T", argName, tool.Name, desc)
			}
			propOpts = append(propOpts, mcp.Description(s))
		}
		var typ string
		var strictNullable bool
		if v, ok := argSchema["type"]; !ok {
			return nil, fmt.Errorf("schema of arg %q of tool %q is missing \"type\": %+v", argName, tool.Name, argSchema)
		} else {
			switch x := v.(type) {
			case string:
				typ = x
			case []string:
				typ = x[0]
				if len(x) < 2 {
					return nil, fmt.Errorf("schema of arg %q of tool %q should have a \"type\" entry of type string or []string with at least two elements, got %T", argName, tool.Name, v)
				}
				if x[1] == "null" {
					strictNullable = true
				} else {
					return nil, fmt.Errorf("schema of arg %q of tool %q should have a \"type\" entry of type string or []string with \"null\" as the second element, got %T", argName, tool.Name, v)
				}
			default:
				return nil, fmt.Errorf("schema of arg %q of tool %q should have a \"type\" entry of type string or []string, got %T", argName, tool.Name, v)
			}
		}

		if v, ok := argSchema["default"]; ok {
			propOpts = append(propOpts, mcpDefaultAny(v))
		}
		if slices.Contains(required, argName) && !strictNullable {
			propOpts = append(propOpts, mcp.Required())
		}

		var mcpArg func(string, ...mcp.PropertyOption) mcp.ToolOption
		switch typ {
		case "array":
			items, ok := argSchema["items"]
			if !ok {
				return nil, fmt.Errorf("schema of array arg %q of tool %q should have an \"items\" entry", argName, tool.Name)
			}
			// TODO: verify items has a valid schema: {"type": string} ? At least OpenAI requires it.
			mcpArg = mcp.WithArray
			propOpts = append(propOpts, mcp.Items(items))
		case "boolean":
			mcpArg = mcp.WithBoolean
		case "integer":
			mcpArg = mcp.WithNumber
		case "number":
			mcpArg = mcp.WithNumber
		case "object":
			mcpArg = mcp.WithObject
		case "string":
			// TODO: should we do anything fancy if argSchema["format"] is present (e.g., ID or CustomType)?
			mcpArg = mcp.WithString
		default:
			return nil, fmt.Errorf("arg %q of tool %q is of unsupported type %q", argName, tool.Name, typ)
		}
		toolOpts = append(toolOpts, mcpArg(argName, propOpts...))
	}
	return toolOpts, nil
}

type mcpServer struct {
	server *mcpserver.MCPServer
	dag    *dagql.Server
	mcp    *MCP
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

		if err := s.setTools(); err != nil {
			return nil, err
		}

		return mcp.NewToolResultText(text), nil
	}
}

func (s mcpServer) convertToMcpTools(llmTools []LLMTool) ([]mcpserver.ServerTool, error) {
	println("🍎 convertToMcpTools")
	defer println("🍎 convertToMcpTools returned")
	mcpTools := make([]mcpserver.ServerTool, 0, len(llmTools))
	for _, tool := range llmTools {
		// Skipping methods that return ID
		if strings.HasSuffix(tool.Name, "_id") {
			continue
		}

		toolOpts, err := genMcpToolOpts(tool)
		if err != nil {
			return nil, err
		}
		mcpTools = append(mcpTools, mcpserver.ServerTool{Tool: mcp.NewTool(tool.Name, toolOpts...), Handler: s.genMcpToolHandler(tool)})
	}
	return mcpTools, nil
}

func (s mcpServer) setTools() error {
	println("🍎 setTools")
	tools, err := s.mcp.Tools(s.dag)
	println("🍎 setTools returned")
	if err != nil {
		return fmt.Errorf("failed to get tools: %w", err)
	}
	mcpTools, err := s.convertToMcpTools(tools)
	if err != nil {
		return fmt.Errorf("failed to convert tools to MCP: %w", err)
	}
	name := ""
	if len(mcpTools) > 0 {
		name = mcpTools[0].Tool.Name
	}
	println("🍎🍎 setting tools", len(mcpTools), name)
	s.server.SetTools(mcpTools...)
	return nil
}

func (s mcpServer) serveStdio(ctx context.Context, pipe io.ReadWriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdioSrv := mcpserver.NewStdioServer(s.server)

	// MCP library requires standard log package
	logger := stdlog.New(bklog.G(ctx).Writer(), "", 0)
	stdioSrv.SetErrorLogger(logger)

	// Set tools lazily because dagger module may take a while to be loaded
	// but we still want to serve stdio ASAP to avoid MCP clients' timeout.
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := s.setTools(); err != nil {
			select {
			case <-ctx.Done():
			case errCh <- err:
				if err != nil {
					cancel()
				}
			}
		}
	}()

	// Start MCP server in a goroutine
	err := stdioSrv.Listen(ctx, pipe, pipe)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (m *MCP) Serve(ctx context.Context, dag *dagql.Server) error {
	s := mcpServer{
		mcpserver.NewMCPServer("Dagger", "0.0.1",
			mcpserver.WithInstructions(defaultSystemPrompt),
			mcpserver.WithToolCapabilities(true),
		),
		dag,
		m,
	}

	// Get buildkit client
	bk, err := m.query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client error: %w", err)
	}

	rwc, err := bk.OpenPipe(ctx)
	if err != nil {
		return fmt.Errorf("open pipe error: %w", err)
	}

	return s.serveStdio(ctx, rwc)
}
