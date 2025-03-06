package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/bbi"
	"github.com/googleapis/gax-go/v2/apierror"
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

	for _, msg := range history {
		// Valid Content.Role values are "user" and "model"
		var role string
		switch msg.Role {
		case "user", "function":
			role = "user"
		case "model", "assistant":
			role = "model"
		default:
			return nil, fmt.Errorf("unexpected role %s", msg.Role)
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

	// Pop last message from history for SendMessage
	userMessage := genaiHistory[len(genaiHistory)-1]
	genaiHistory = genaiHistory[:len(genaiHistory)-1]

	chat := model.StartChat()
	chat.History = genaiHistory

	resp, err := chat.SendMessage(ctx, userMessage.Parts...)
	if err != nil {
		if apiErr, ok := err.(*apierror.APIError); ok {
			// unwrap the APIError
			return nil, fmt.Errorf("google API error occurred: %w", apiErr.Unwrap())
		}
		return nil, err
	}

	// TODO: add tracing

	// process response
	var content string
	var toolCalls []ToolCall
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				// check if tool call
				switch part := part.(type) {
				case genai.FunctionCall:
					toolCalls = append(toolCalls, ToolCall{
						ID: part.Name,
						Function: FuncCall{
							Name:      part.Name,
							Arguments: part.Args,
						},
						Type: "function",
					})
				case genai.Text:
					content += string(part)
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
