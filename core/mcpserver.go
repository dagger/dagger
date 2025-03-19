package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/moby/buildkit/util/bklog"
)

// mcpDefaultArray is a stop-gap until mcp.DefaultArray exists upstream.
func mcpDefaultArray[T any](v []T) mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["default"] = v
	}
}

func genMcpToolOpts(tool LLMTool) ([]mcp.ToolOption, error) {
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
		if v, ok := argSchema["type"]; !ok {
			return nil, fmt.Errorf("schema of arg %q of tool %q is missing \"type\": %+v", argName, tool.Name, argSchema)
		} else {
			typ, ok = v.(string)
			if !ok {
				return nil, fmt.Errorf("schema of arg %q of tool %q should have a \"type\" entry of type string, got %T", argName, tool.Name, v)
			}
		}

		var defaultVal *string
		if v, ok := argSchema["default"]; ok {
			defVal, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("only \"string\" is currently supported for the default value of arg %q of tool %q, got %T", argName, tool.Name, v)
			}
			defaultVal = &defVal
		}
		if slices.Contains(required, argName) {
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
			if defaultVal != nil {
				if *defaultVal != "[]" && *defaultVal != "" {
					return nil, fmt.Errorf("not implemented: default value of array arg %q of tool %q can only be an empty array, received: %s", argName, tool.Name, *defaultVal)
				}
				propOpts = append(propOpts, mcpDefaultArray([]any{}))
			}
		case "boolean":
			mcpArg = mcp.WithBoolean
			if defaultVal != nil {
				b, err := strconv.ParseBool(*defaultVal)
				if err != nil {
					return nil, fmt.Errorf("failed to parse default value of boolean arg %q of tool %q: %w", argName, tool.Name, err)
				}
				propOpts = append(propOpts, mcp.DefaultBool(b))
			}
		case "integer":
			mcpArg = mcp.WithNumber
			if defaultVal != nil {
				i, err := strconv.ParseInt(*defaultVal, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("failed to parse default value of integer arg %q of tool %q: %w", argName, tool.Name, err)
				}
				propOpts = append(propOpts, mcp.DefaultNumber(float64(i)))
			}
		case "number":
			mcpArg = mcp.WithNumber
			if defaultVal != nil {
				f, err := strconv.ParseFloat(*defaultVal, 64)
				if err != nil {
					return nil, fmt.Errorf("failed to parse default value of number arg %q of tool %q: %w", argName, tool.Name, err)
				}
				propOpts = append(propOpts, mcp.DefaultNumber(f))
			}
		case "string":
			// TODO: should we do anything fancy if argSchema["format"] is present (e.g., ID or CustomType)?
			mcpArg = mcp.WithString
			if defaultVal != nil {
				propOpts = append(propOpts, mcp.DefaultString(*defaultVal))
			}
		default:
			return nil, fmt.Errorf("arg %q of tool %q is of unsupported type %q", argName, tool.Name, typ)
		}
		toolOpts = append(toolOpts, mcpArg(argName, propOpts...))
	}
	return toolOpts, nil
}

func (llm *LLM) MCP(ctx context.Context, dag *dagql.Server) error {
	s := mcpserver.NewMCPServer("Dagger", "0.0.1")

	var genMcpToolHandler func(LLMTool) mcpserver.ToolHandlerFunc
	genMcpToolHandler = func(tool LLMTool) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// should never happen
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

			newTools, err := llm.env.Tools(dag)
			if err != nil {
				return nil, fmt.Errorf("failed to get tools: %w", err)
			}
			mcpTools := make([]mcpserver.ServerTool, 0, len(newTools))
			for _, tool := range newTools {
				// Skipping methods that return ID
				if strings.HasSuffix(tool.Name, "_id") {
					continue
				}

				toolOpts, err := genMcpToolOpts(tool)
				if err != nil {
					return nil, err
				}
				mcpTools = append(mcpTools, mcpserver.ServerTool{Tool: mcp.NewTool(tool.Name, toolOpts...), Handler: genMcpToolHandler(tool)})
			}
			go s.AddTools(mcpTools...)

			return mcp.NewToolResultText(text), nil
		}
	}

	addTool := func(tool LLMTool) error {
		mcpToolOpts, err := genMcpToolOpts(tool)
		if err != nil {
			return err
		}

		s.AddTool(
			mcp.NewTool(tool.Name, mcpToolOpts...),
			genMcpToolHandler(tool),
		)

		return nil
	}

	tools, err := llm.env.Tools(dag)
	if err != nil {
		return fmt.Errorf("failed to get tools: %w", err)
	}
	for _, tool := range tools {
		if err := addTool(tool); err != nil {
			return err
		}
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

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)

	srv := mcpserver.NewStdioServer(s)

	// MCP library requires standard log package
	logger := stdlog.New(bklog.G(ctxWithCancel).Writer(), "", 0)
	srv.SetErrorLogger(logger)

	// Start MCP server in a goroutine
	go func() {
		err := srv.Listen(ctxWithCancel, rwc, rwc)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			errCh <- fmt.Errorf("MCP server error: %w", err)
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
