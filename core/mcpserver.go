package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"reflect"
	"slices"
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

func errTypeMismatch(typ string, defaultVal any, argName, toolName string) error {
	return fmt.Errorf("arg %q of tool %q has a \"type\" field (%q) that is incompatible with the type of the \"default\" field (%T)", argName, toolName, typ, defaultVal)
}

func toFloat64(n any) (float64, bool) {
	v := reflect.ValueOf(n)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	default:
		return 0, false
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

		defaultVal := argSchema["default"]
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
				s, _ := defaultVal.(string)
				if s != "" && s != "[]" {
					return nil, fmt.Errorf("not implemented: default value of array arg %q of tool %q can only be the string \"[]\", received: %v", argName, tool.Name, defaultVal)
				}
				propOpts = append(propOpts, mcpDefaultArray([]any{}))
			}
		case "boolean":
			mcpArg = mcp.WithBoolean
			if defaultVal != nil {
				b, ok := defaultVal.(bool)
				if !ok {
					return nil, errTypeMismatch(typ, defaultVal, argName, tool.Name)
				}
				propOpts = append(propOpts, mcp.DefaultBool(b))
			}
		case "integer", "number":
			mcpArg = mcp.WithNumber
			if defaultVal != nil {
				f, ok := toFloat64(defaultVal)
				if !ok {
					return nil, errTypeMismatch(typ, defaultVal, argName, tool.Name)
				}
				propOpts = append(propOpts, mcp.DefaultNumber(f))
			}
		case "string":
			// TODO: should we do anything fancy if argSchema["format"] is present (e.g., ID or CustomType)?
			mcpArg = mcp.WithString
			if defaultVal != nil {
				s, ok := defaultVal.(string)
				if !ok {
					return nil, errTypeMismatch(typ, defaultVal, argName, tool.Name)
				}
				propOpts = append(propOpts, mcp.DefaultString(s))
			}
		default:
			return nil, fmt.Errorf("arg %q of tool %q is of unsupported type %q", argName, tool.Name, typ)
		}
		toolOpts = append(toolOpts, mcpArg(argName, propOpts...))
	}
	return toolOpts, nil
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

		if err := s.setTools(); err != nil {
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

		toolOpts, err := genMcpToolOpts(tool)
		if err != nil {
			return nil, err
		}
		mcpTools = append(mcpTools, mcpserver.ServerTool{Tool: mcp.NewTool(tool.Name, toolOpts...), Handler: s.genMcpToolHandler(tool)})
	}
	return mcpTools, nil
}

func (s mcpServer) setTools() error {
	tools, err := s.env.Tools(s.dag)
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

	if err := s.setTools(); err != nil {
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
	// Get buildkit client
	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client error: %w", err)
	}

	rwc, err := bk.OpenPipe(ctx)
	if err != nil {
		return fmt.Errorf("open pipe error: %w", err)
	}

	s := mcpServer{
		mcpserver.NewMCPServer("Dagger", "0.0.1"),
		dag,
		llm.mcp,
		rwc,
	}

	return s.run(ctx)
}
