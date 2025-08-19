package core

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/go-cmp/cmp"
)

type LLMReplayer struct {
	messages []*ModelMessage
}

func newHistoryReplay(messages []*ModelMessage) *LLMReplayer {
	return &LLMReplayer{messages: messages}
}

func (*LLMReplayer) IsRetryable(err error) bool {
	return false
}

func (c *LLMReplayer) SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	if len(history) > 0 && history[0].Role == "system" {
		// HACK: drop the default system prompt, since we don't return it in
		// HistoryJSON
		history = history[1:]
	}
	if len(history) >= len(c.messages) {
		return nil, fmt.Errorf("no more messages")
	}
	for i, message := range history {
		// TODO: (cwlbraa) is this a complete comparison? also doesn't this end up being O(n^2)?
		if stabilizeContent(message.Content) != stabilizeContent(c.messages[i].Content) || message.Role != c.messages[i].Role {
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

var xxh3Regexp = regexp.MustCompile(`@xxh3:[a-f0-9]{16}`)
var traceIDRegexp = regexp.MustCompile(`[a-f0-9]{2}-[a-f0-9]{32}-[a-f0-9]{16}-[a-f0-9]{2}`)

func stabilizeContent(content string) string {
	content = xxh3Regexp.ReplaceAllString(content, "@xxh3:0000000000000000")
	content = traceIDRegexp.ReplaceAllString(content, "00-00000000000000000000000000000000-0000000000000000-00")
	return content
}
