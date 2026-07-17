package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/util/scrub"
	"github.com/google/go-cmp/cmp"
)

// replayMessage mirrors the JSON shape of a conversation exported with the
// v1 `messages` field (GraphQL's lowerCamel key spelling), which is the
// recording format consumed by replay/ models.
type replayMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		CallID    string `json:"callId"`
		ToolName  string `json:"toolName"`
		Arguments string `json:"arguments"`
		Errored   bool   `json:"errored"`
		Signature string `json:"signature"`
	} `json:"content"`
	TokenUsage struct {
		InputTokens       int64 `json:"inputTokens"`
		OutputTokens      int64 `json:"outputTokens"`
		CachedTokenReads  int64 `json:"cachedTokenReads"`
		CachedTokenWrites int64 `json:"cachedTokenWrites"`
		TotalTokens       int64 `json:"totalTokens"`
	} `json:"tokenUsage"`
}

// decodeReplayMessages parses a replay recording into message history.
func decodeReplayMessages(data []byte) ([]*LLMMessage, error) {
	var wire []replayMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	messages := make([]*LLMMessage, len(wire))
	for i, m := range wire {
		msg := &LLMMessage{
			Role: LLMMessageRole(m.Role),
			TokenUsage: &LLMTokenUsage{
				InputTokens:       m.TokenUsage.InputTokens,
				OutputTokens:      m.TokenUsage.OutputTokens,
				CachedTokenReads:  m.TokenUsage.CachedTokenReads,
				CachedTokenWrites: m.TokenUsage.CachedTokenWrites,
				TotalTokens:       m.TokenUsage.TotalTokens,
			},
		}
		for _, b := range m.Content {
			msg.Content = append(msg.Content, &LLMContentBlock{
				Kind:      LLMContentBlockKind(b.Kind),
				Text:      b.Text,
				CallID:    b.CallID,
				ToolName:  b.ToolName,
				Arguments: JSON(b.Arguments),
				Errored:   b.Errored,
				Signature: b.Signature,
			})
		}
		messages[i] = msg
	}
	return messages, nil
}

type LLMReplayer struct {
	messages []*LLMMessage
}

func newHistoryReplay(messages []*LLMMessage) *LLMReplayer {
	return &LLMReplayer{messages: messages}
}

func (*LLMReplayer) IsRetryable(err error) bool {
	return false
}

func (c *LLMReplayer) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, _ *LLMCallOpts) (_ *LLMResponse, rerr error) {
	if len(history) > 0 && history[0].Role == LLMMessageRoleSystem {
		// HACK: drop the default system prompt, since recordings only contain
		// the message history exported via messages, not the synthesized
		// system prompt
		history = history[1:]
	}
	if len(history) >= len(c.messages) {
		return nil, fmt.Errorf("no more messages")
	}
	for i, message := range history {
		// TODO: (cwlbraa) is this a complete comparison? also doesn't this end up being O(n^2)?
		if scrub.Stabilize(message.TextContent()) != scrub.Stabilize(c.messages[i].TextContent()) || message.Role != c.messages[i].Role {
			return nil, fmt.Errorf(
				"message history diverges at index %d:\n%s",
				i,
				cmp.Diff(c.messages[i], message),
			)
		}
	}
	msg := c.messages[len(history)]

	res := &LLMResponse{
		Content: msg.Content,
	}
	if msg.TokenUsage != nil {
		res.TokenUsage = *msg.TokenUsage
	}
	return res, nil
}
