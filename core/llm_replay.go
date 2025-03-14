package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/bbi"
)

type LLMReplayer struct {
	messages []ModelMessage
}

func newHistoryReplay(messages []ModelMessage) *LLMReplayer {
	return &LLMReplayer{messages: messages}
}

func (c *LLMReplayer) SendQuery(ctx context.Context, history []ModelMessage, tools []bbi.Tool) (_ *LLMResponse, rerr error) {
	if len(history) >= len(c.messages) {
		return nil, fmt.Errorf("no more messages")
	}
	for i, message := range history {
		if message.Content != c.messages[i].Content || message.Role != c.messages[i].Role {
			// FIXME: improve comparisons
			return nil, fmt.Errorf("message history diverges at %d", i)
		}
	}
	msg := c.messages[len(history)]

	content, err := msg.Text()
	if err != nil {
		return nil, err
	}
	return &LLMResponse{
		Content:    content,
		ToolCalls:  msg.ToolCalls,
		TokenUsage: msg.TokenUsage,
	}, nil
}
