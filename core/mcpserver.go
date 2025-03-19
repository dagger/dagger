package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/moby/buildkit/util/bklog"
)

func (llm *LLM) MCP(ctx context.Context, dag *dagql.Server) error {
	bklog.G(ctx).Debugf("🎃 Starting MCP function")

	// Create MCP server with tool
	s := mcpserver.NewMCPServer("Dagger", "0.0.1")

	genToolOpts := func(tool LLMTool) ([]mcp.ToolOption, error) {
		toolOpts := []mcp.ToolOption{
			mcp.WithDescription(tool.Description),
		}
		if err := (func(tool LLMTool) error {
			var required []string
			if v, ok := tool.Schema["required"]; ok {
				required, ok = v.([]string)
				if !ok {
					return fmt.Errorf("expecting type []string for \"required\" for tool %q", tool.Name)
				}
			}
			props, ok := tool.Schema["properties"]
			if !ok {
				return fmt.Errorf("schema of tool %q is missing \"properties\": %+v", tool.Name, tool.Schema)
			}
			for argName, v := range props.(map[string]interface{}) {
				var propOpts []mcp.PropertyOption
				argSchema := v.(map[string]interface{})
				if desc, ok := argSchema["description"]; ok {
					s, ok := desc.(string)
					if !ok {
						return fmt.Errorf("description of arg %q of tool %q is expected to be of type string, but is %T", argName, tool.Name, desc)
					}
					propOpts = append(propOpts, mcp.Description(s))
				}
				var typ string
				if v, ok := argSchema["type"]; !ok {
					return fmt.Errorf("schema of arg %q of tool %q is missing \"type\": %+v", argName, tool.Name, argSchema)
				} else {
					typ, ok = v.(string)
					if !ok {
						return fmt.Errorf("schema of arg %q of tool %q should have a \"type\" entry of type string, got %T", argName, tool.Name, v)
					}
				}

				if v, ok := argSchema["default"]; ok {
					// TODO: unclear why default uses DefaultValue.Raw string. What about other types?
					if typ != "string" {
						return fmt.Errorf("arg %q of tool %q is of type %q but has a default of type \"string\"", argName, tool.Name, typ)
					}
					defaultVal, ok := v.(string)
					if !ok {
						return fmt.Errorf("only \"string\" is currently supported for the default value of arg %q of tool %q, got %T", v)
					}
					propOpts = append(propOpts, mcp.DefaultString(defaultVal))
				}
				for _, r := range required {
					if r == argName {
						propOpts = append(propOpts, mcp.Required())
						break
					}
				}

				var mcpArg func(name string, propOpts ...mcp.PropertyOption) mcp.ToolOption
				switch typ {
				case "array":
					if _, ok := argSchema["items"]; !ok {
						return fmt.Errorf("schema of array arg %q of tool %q should have an \"items\" entry", argName, tool.Name)
					}
					// TODO: need some recursion: array of array ...
					return fmt.Errorf("[MCP] array type not implemented")
				case "boolean":
					mcpArg = mcp.WithBoolean
				case "integer":
					mcpArg = mcp.WithNumber
				case "number":
					mcpArg = mcp.WithNumber
				case "string":
					// TODO: should ID and custom type, use mcp.WithObject ?
					mcpArg = mcp.WithString
				}
				toolOpts = append(toolOpts, mcpArg(argName, propOpts...))
			}
			return nil
		})(tool); err != nil {
			return nil, err
		}
		return toolOpts, nil
	}

	tools, err := llm.env.Tools(dag)
	if err != nil {
		return fmt.Errorf("failed to get tools: %w", err)
	}
	for _, tool := range tools {
		toolOpts, err := genToolOpts(tool)
		if err != nil {
			return err
		}

		// inception :smirk:
		// In order to have compatibility between LLM and MCP, we want -> on any given MCP tooling response, to also signify that
		var toolHandler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
		toolHandler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if request.Method != "tools/call" {
				return nil, fmt.Errorf("[dagger] expected MCP request method \"tools/call\" but received %q", request.Method)
			}

			result, err := tool.Call(ctx, request.Params.Arguments)
			// TODO: differentiate user module's error from dagger error for better error message
			if err != nil {
				return nil, fmt.Errorf("tool %q called with %v resulted in error: %w", tool.Name, request.Params.Arguments, err)
			}
			text, ok := result.(string)
			if !ok {
				b, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("[dagger] could not JSON marshal result %+v: %w", result, err)
				}
				text = string(b)
			}

			tools, err := llm.env.Tools(dag)
			if err != nil {
				return nil, err
			}
			for _, tool := range tools {
				toolOpts, err := genToolOpts(tool)
				if err != nil {
					return nil, err
				}
				s.AddTool(mcp.NewTool(tool.Name, toolOpts...), toolHandler)
			}

			return mcp.NewToolResultText(text), nil
		}

		s.AddTool(
			mcp.NewTool(tool.Name, toolOpts...),
			toolHandler,
		)
	}

	// Get buildkit client
	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client error: %w", err)
	}

	rwc, err := bk.OpenPipe(ctx)
	if err != nil {
		return fmt.Errorf("open pipe error: %w", err)
	}
	defer rwc.Close()
	bklog.G(ctx).Debugf("🎃 Pipe opened")

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)

	// Create MCP server
	srv := mcpserver.NewStdioServer(s)

	// Use standard log package
	logger := stdlog.New(bklog.G(ctxWithCancel).Writer(), "", 0)
	srv.SetErrorLogger(logger)

	// Start MCP server in a goroutine
	go func() {
		bklog.G(ctxWithCancel).Debugf("🎃 Starting MCP server listener")
		err := srv.Listen(ctxWithCancel, rwc, rwc)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			bklog.G(ctxWithCancel).Errorf("🎃 MCP server error: %v", err)
			errCh <- fmt.Errorf("MCP server error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		bklog.G(ctx).Debugf("🎃 Context done, shutting down")
		return ctx.Err()
	case err := <-errCh:
		bklog.G(ctx).Errorf("🎃 Error in goroutine: %v", err)
		return err
	}
}
