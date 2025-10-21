package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/util/scrub"
	"github.com/google/go-cmp/cmp"
)

type LLMReplayer struct {
	messages []*LLMMessage
}

func newHistoryReplay(messages []*LLMMessage) *LLMReplayer {
	return &LLMReplayer{messages: messages}
}

func (*LLMReplayer) IsRetryable(err error) bool {
	return false
}

func (c *LLMReplayer) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	if len(history) > 0 && history[0].Role == LLMMessageRoleSystem {
		// HACK: drop the default system prompt, since we don't return it in
		// HistoryJSON
		history = history[1:]
	}
	if len(history) >= len(c.messages) {
		return nil, fmt.Errorf("no more messages")
	}
	for i, message := range history {
		// TODO: (cwlbraa) is this a complete comparison? also doesn't this end up being O(n^2)?
		if scrub.Stabilize(message.Content) != scrub.Stabilize(c.messages[i].Content) || message.Role != c.messages[i].Role {
			return nil, fmt.Errorf(
				"message history diverges at index %d:\n%s",
				i,
				cmp.Diff(c.messages[i], message),
			)
		}
	}
	msg := c.messages[len(history)]

	return &LLMResponse{
		Content:    msg.Content,
		ToolCalls:  msg.ToolCalls,
		TokenUsage: msg.TokenUsage,
	}, nil
}
