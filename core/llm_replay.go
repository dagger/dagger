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

func (c *LLMReplayer) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
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

	// Build the same per-block display spans a streaming provider builds, so a
	// replayed turn produces telemetry identical to a live one: thinking, text
	// response, and one span per tool call (carrying the Boundary/roll-up
	// attributes the evaluation loop needs to nest the tool's execution beneath
	// its own call). Without these, every replayed tool call would run under the
	// shared loop context and every replay-driven test would exercise a shape
	// production never has.
	//
	// Note this is the *live loop* path (model `replay/…`), which is distinct
	// from LLM.Replay: that one re-emits spans for an already-recorded
	// conversation for display only, and never runs tools. The two never both
	// emit spans for the same tool call.
	var callDigest string
	if opts != nil {
		callDigest = opts.CallDigest
	}
	dp := newDisplayPhases(ctx, callDigest)
	defer func() {
		dp.CloseAll()
		if rerr != nil {
			dp.Abort(rerr)
		}
	}()
	for i, block := range msg.Content {
		idx := int64(i)
		switch block.Kind {
		case LLMContentThinking:
			if block.Text == "" {
				continue
			}
			p := dp.StartThinking(idx)
			fmt.Fprint(p.Stdio.Stdout, block.Text)
			dp.Close(idx)
		case LLMContentText:
			if block.Text == "" {
				continue
			}
			p := dp.StartText(idx)
			fmt.Fprint(p.MarkdownW, block.Text)
			dp.Close(idx)
		case LLMContentToolCall:
			// The tool-call span stays open; the loop ends it (with the
			// result-size badge) via endToolCallDisplay once the tool returns.
			dp.EmitToolCall(idx, block.CallID, block.ToolName, string(block.Arguments))
		}
	}

	displaySpans, toolCallDisplays := dp.Response()
	res := &LLMResponse{
		Content:          msg.Content,
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCallDisplays,
	}
	if msg.TokenUsage != nil {
		res.TokenUsage = *msg.TokenUsage
	}
	return res, nil
}
