package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/bbi"
	"github.com/google/go-cmp/cmp"
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
		// TODO: (cwlbraa) is this a complete comparison? also doesn't this end up being O(n^2)?
		if message.Content != c.messages[i].Content || message.Role != c.messages[i].Role {
			return nil, fmt.Errorf(
				"message history diverges at index %d:\n%s",
				i,
				cmp.Diff(c.messages[i], message),
			)
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
