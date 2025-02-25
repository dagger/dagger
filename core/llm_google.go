package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/bbi"
	"google.golang.org/api/option"

	genai "github.com/google/generative-ai-go/genai"
)

type GenaiClient struct {
	client              *genai.Client
	endpoint            *LlmEndpoint
	defaultSystemPrompt string
}

func newGenaiClient(endpoint *LlmEndpoint, defaultSystemPrompt string) (*GenaiClient, error) {
	opts := []option.ClientOption{option.WithAPIKey(endpoint.Key)}
	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithEndpoint(endpoint.BaseURL))
	}
	ctx := context.Background() // FIXME: should we wire this through from somewhere else?
	client, err := genai.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &GenaiClient{
		client:              client,
		endpoint:            endpoint,
		defaultSystemPrompt: defaultSystemPrompt,
	}, err
}

func (c *GenaiClient) SendQuery(ctx context.Context, history []ModelMessage, tools []bbi.Tool) (*LLMResponse, error) {
	// set model
	model := c.client.GenerativeModel(c.endpoint.Model)

	// set system prompt
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(c.defaultSystemPrompt)},
		Role:  "system",
	}

	// convert tools to Genai tool format.
	var toolsConfig []*genai.Tool
	for _, tool := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		schema := bbiSchemaToGenaiSchema(tool.Schema)
		// only add Parameters if there are any
		if len(schema.Properties) > 0 {
			fd.Parameters = schema
		}
		toolsConfig = append(toolsConfig, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				fd,
			},
		})
	}
	model.Tools = toolsConfig

	// convert history to genai.Content
	genaiHistory := []*genai.Content{}
	if len(history) == 0 {
		return nil, fmt.Errorf("genai history cannot be empty")
	}
	userMessage := history[len(history)-1]
	if userMessage.Role != "user" {
		// Genai expects the last message to be a user message
		return nil, fmt.Errorf("expected last message to be a user message, got %s", history[len(history)].Role)
	}
	// exclude the final user message
	for _, msg := range history[:len(history)-1] {
		// Valid Content.Role values are "user" and "model"
		role := "user"
		if msg.Role != "user" {
			role = "model"
		}
		content := &genai.Content{
			Parts: []genai.Part{},
			Role:  role,
		}

		// message was a tool call
		if msg.ToolCallID != "" {
			// find the function name
			content.Parts = append(content.Parts, genai.FunctionResponse{
				Name: msg.ToolCallID,
				// Genai expects a json format response
				Response: map[string]any{
					"response": msg.Content.(string),
					"error":    msg.ToolErrored,
				},
			})
		} else { // just content
			c := msg.Content.(string)
			if c == "" {
				c = " "
			}
			content.Parts = append(content.Parts, genai.Text(c))
		}

		// add tool calls
		for _, call := range msg.ToolCalls {
			content.Parts = append(content.Parts, genai.FunctionCall{
				Name: call.ID,
				Args: call.Function.Arguments,
			})
		}

		genaiHistory = append(genaiHistory, content)
	}

	chat := model.StartChat()
	chat.History = genaiHistory

	userMessageStr := userMessage.Content.(string)
	if userMessageStr == "" {
		userMessageStr = " "
	}

	resp, err := chat.SendMessage(ctx, genai.Text(userMessageStr))
	if err != nil {
		return nil, err
	}

	// TODO: add tracing

	// process response
	var content string
	var toolCalls []ToolCall
	for _, candidate := range resp.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				// check if tool call
				switch part.(type) {
				case genai.FunctionCall:
					toolCalls = append(toolCalls, ToolCall{
						ID: part.(genai.FunctionCall).Name,
						Function: FuncCall{
							Name:      part.(genai.FunctionCall).Name,
							Arguments: part.(genai.FunctionCall).Args,
						},
						Type: "function",
					})
				case genai.Text:
					content += string(part.(genai.Text))
				default:
					return nil, fmt.Errorf("unexpected genai part type %T", part)
				}
			}
		}
	}

	return &LLMResponse{
		Content:   content,
		ToolCalls: toolCalls,
	}, nil
}

// TODO: this definitely needs a unit test
func bbiSchemaToGenaiSchema(bbi map[string]interface{}) *genai.Schema {
	schema := &genai.Schema{}
	for key, param := range bbi {
		switch key {
		case "description":
			schema.Description = param.(string)
		case "properties":
			schema.Properties = map[string]*genai.Schema{}
			for propKey, propParam := range param.(map[string]interface{}) {
				schema.Properties[propKey] = bbiSchemaToGenaiSchema(propParam.(map[string]interface{}))
			}
		case "default": // just setting Nullable=true. Genai Schema does not have Default
			schema.Nullable = true
		case "type":
			gtype := bbiTypeToGenaiType(param.(string))
			schema.Type = gtype
		// case "format": // ignoring format. Genai is very picky about format values
		// 	schema.Format = param.(string)
		case "items":
			schema.Items = bbiSchemaToGenaiSchema(param.(map[string]interface{}))
		case "required":
			schema.Required = bbi["required"].([]string)
		}
	}

	return schema
}

func bbiTypeToGenaiType(bbi string) genai.Type {
	switch bbi {
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "string":
		return genai.TypeString
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default: // should not happen
		return genai.TypeUnspecified
	}
}
