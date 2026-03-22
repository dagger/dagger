package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// loadMCPTools loads tools from external MCP servers.
func (m *MCP) loadMCPTools(ctx context.Context, allTools *LLMToolSet) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("get current query: %w", err)
	}
	mcps, err := query.MCPClients(ctx)
	if err != nil {
		return fmt.Errorf("get mcp clients: %w", err)
	}
	for serverName, cfg := range m.mcpServers {
		sess, err := mcps.Dial(ctx, cfg)
		if err != nil {
			return fmt.Errorf("dial mcp server %q: %w", serverName, err)
		}
		for tool, err := range sess.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			schema, err := toAny(tool.InputSchema)
			if err != nil {
				return err
			}
			if schema["properties"] == nil {
				schema["properties"] = map[string]any{}
			}

			isReadOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint

			allTools.Add(LLMTool{
				Name:        tool.Name,
				Server:      serverName,
				Description: tool.Description,
				Schema:      schema,
				ReadOnly:    isReadOnly,
				Call: func(ctx context.Context, args any) (any, error) {
					res, err := sess.CallTool(ctx, &mcp.CallToolParams{
						Name:      tool.Name,
						Arguments: args,
					})
					if err != nil {
						return nil, fmt.Errorf("call tool %q on mcp %q: %w", tool.Name, serverName, err)
					}

					var out string
					for _, content := range res.Content {
						switch x := content.(type) {
						case *mcp.TextContent:
							out += x.Text
						default:
							out += fmt.Sprintf("WARNING: unsupported content type %T", x)
						}
					}
					if res.StructuredContent != nil {
						str, err := toolStructuredResponse(res.StructuredContent)
						if err != nil {
							return nil, err
						}
						out += str
					}
					if res.IsError {
						return "", errors.New(out)
					}
					return out, nil
				},
			})
		}
	}
	return nil
}
