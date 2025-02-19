package core

// type AnthropicClient struct {
// 	client       *anthropic.Client
// 	config       *LlmConfig
// 	systemPrompt string
// }

// func newAnthropicClient(config *LlmConfig) *AnthropicClient {
// 	opts := []option.RequestOption{option.WithAPIKey(config.Key)}
// 	if config.Host != "" {
// 		opts = append(opts, option.WithBaseURL(config.Host))
// 	}
// 	client := anthropic.NewClient(opts...)
// 	return &AnthropicClient{
// 		client: client,
// 		config: config,
// 	}
// }

// func (c *AnthropicClient) SendQuery(ctx context.Context, history []Message, tools []bbi.Tool) (*LLMResponse, error) {
// 	// Convert generic Message to Anthropic specific format
// 	var messages []anthropic.MessageParam
// 	for _, msg := range history {
// 		switch msg.Role {
// 		case "user":
// 			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content.(string))))
// 		case "assistant":
// 			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content.(string))))
// 		case "system":
// 			c.systemPrompt = msg.Content.(string)
// 		}
// 	}

// 	// Convert tools to Anthropic tool format
// 	var toolsConfig []anthropic.ToolParam
// 	for _, tool := range tools {
// 		toolsConfig = append(toolsConfig, anthropic.ToolParam{
// 			Name:        anthropic.F(tool.Name),
// 			Description: anthropic.F(tool.Description),
// 			InputSchema: anthropic.F(interface{}(tool.Schema)),
// 		})
// 	}

// 	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
// 		Model:    anthropic.F(c.config.Model),
// 		Messages: anthropic.F(messages),
// 		Tools:    anthropic.F(toolsConfig),
// 		System:   anthropic.F([]anthropic.TextBlockParam{anthropic.NewTextBlock(c.systemPrompt)}),
// 	})
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Convert Anthropic response to generic LLMResponse
// 	var toolCalls []ToolCall
// 	var content string

// 	for _, block := range resp.Content {
// 		switch block := block.AsUnion().(type) {
// 		case anthropic.TextBlock:
// 			content = block.Text
// 		case anthropic.ToolUseBlock:
// 			toolCalls = append(toolCalls, ToolCall{
// 				ID: block.ID,
// 				Function: FuncCall{
// 					Name:      block.Name,
// 					Arguments: string(block.Input),
// 				},
// 				Type: "function",
// 			})
// 		}
// 	}

// 	return &LLMResponse{
// 		Content:   content,
// 		ToolCalls: toolCalls,
// 	}, nil
// }
