package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/util/scrub"
	"github.com/google/go-cmp/cmp"
)

// recordedMessage mirrors the JSON shape of a conversation exported with the
// v1 `messages` field (GraphQL's lowerCamel key spelling), which is the
// recording format consumed by recording/ models.
type recordedMessage struct {
	Role    string                 `json:"role"`
	Content []recordedContentBlock `json:"content"`
	Origin  *struct {
		Kind      LLMMessageOriginKind `json:"kind"`
		AgentName string               `json:"agentName"`
		Ref       string               `json:"ref"`
		ReplyTo   string               `json:"replyTo"`
	} `json:"origin"`
	TokenUsage struct {
		InputTokens       int64 `json:"inputTokens"`
		OutputTokens      int64 `json:"outputTokens"`
		CachedTokenReads  int64 `json:"cachedTokenReads"`
		CachedTokenWrites int64 `json:"cachedTokenWrites"`
		TotalTokens       int64 `json:"totalTokens"`
	} `json:"tokenUsage"`
}

type recordedContentBlock struct {
	Kind      string                 `json:"kind"`
	Text      string                 `json:"text"`
	CallID    string                 `json:"callId"`
	ToolName  string                 `json:"toolName"`
	Arguments string                 `json:"arguments"`
	Errored   bool                   `json:"errored"`
	Signature string                 `json:"signature"`
	MIMEType  string                 `json:"mimeType"`
	Data      string                 `json:"data"`
	Content   []recordedContentBlock `json:"content"`
}

func (b recordedContentBlock) block() *LLMContentBlock {
	block := &LLMContentBlock{
		Kind: LLMContentBlockKind(b.Kind), Text: b.Text, CallID: b.CallID,
		ToolName: b.ToolName, Arguments: JSON(b.Arguments), Errored: b.Errored,
		Signature: b.Signature, MIMEType: b.MIMEType, Data: b.Data,
	}
	for _, child := range b.Content {
		block.Content = append(block.Content, child.block())
	}
	return block
}

// decodeRecordedMessages parses a conversation recording into message history.
func decodeRecordedMessages(data []byte) ([]*LLMMessage, error) {
	var wire []recordedMessage
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
		if m.Origin != nil {
			msg.Origin = &LLMMessageOrigin{Kind: m.Origin.Kind, AgentName: m.Origin.AgentName, Ref: m.Origin.Ref, ReplyTo: m.Origin.ReplyTo}
		}
		for _, b := range m.Content {
			msg.Content = append(msg.Content, b.block())
		}
		if err := ValidateLLMContent(msg.Content); err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		messages[i] = msg
	}
	return messages, nil
}

type RecordedResponseProvider struct {
	messages []*LLMMessage
}

func newRecordedResponseProvider(messages []*LLMMessage) *RecordedResponseProvider {
	return &RecordedResponseProvider{messages: messages}
}

func (*RecordedResponseProvider) IsRetryable(err error) bool {
	return false
}

type recordingMediaBlock struct {
	Position, MIMEType, Data string
}

// recordingMedia keeps media identity and ordering separate from the legacy
// stabilized text comparison. Positions include enclosing block kinds so nested
// tool-result media cannot match otherwise identical top-level media.
func recordingMedia(msg *LLMMessage) []recordingMediaBlock {
	var media []recordingMediaBlock
	var walk func([]*LLMContentBlock, string)
	walk = func(blocks []*LLMContentBlock, parent string) {
		for i, block := range blocks {
			if block == nil {
				continue
			}
			position := fmt.Sprintf("%s/%d:%s", parent, i, block.Kind)
			switch block.Kind {
			case LLMContentImage, LLMContentAudio, LLMContentDocument:
				media = append(media, recordingMediaBlock{Position: position, MIMEType: block.MIMEType, Data: block.Data})
			}
			walk(block.Content, position)
		}
	}
	walk(msg.Content, "")
	return media
}

// recordingDiffMessage sanitizes even unchanged neighboring fields before cmp
// builds its context: a text-only mismatch must not print inline media bytes.
func recordingDiffMessage(msg *LLMMessage) *LLMMessage {
	clone := msg.Clone()
	var redact func([]*LLMContentBlock)
	redact = func(blocks []*LLMContentBlock) {
		for _, block := range blocks {
			if block == nil {
				continue
			}
			if block.Data != "" {
				block.Data = "[media data omitted]"
			}
			redact(block.Content)
		}
	}
	redact(clone.Content)
	return clone
}

func (c *RecordedResponseProvider) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
	if len(history) > 0 && history[0].Role == LLMMessageRoleSystem {
		// HACK: drop the synthesized default system prompt, which recordings
		// never contain (they hold only the history exported via messages) —
		// but only when the leading system message is not the recording's OWN
		// leading system message. A conversation composed with an explicit
		// system prompt and no synthesized default (async-agents.md item 15:
		// a staff worker's seed) would otherwise lose its real prompt to this
		// trim and diverge at index 0 forever.
		if len(c.messages) == 0 || c.messages[0].Role != LLMMessageRoleSystem ||
			scrub.Stabilize(c.messages[0].TextContent()) != scrub.Stabilize(history[0].TextContent()) {
			history = history[1:]
		}
	}
	if len(history) >= len(c.messages) {
		return nil, fmt.Errorf("no more messages")
	}
	rendered := renderMessagesForModel(c.messages[:len(history)])
	for i, message := range history {
		mediaMismatch := !cmp.Equal(recordingMedia(rendered[i]), recordingMedia(message))
		if mediaMismatch || scrub.Stabilize(message.TextContent()) != scrub.Stabilize(rendered[i].TextContent()) || message.Role != c.messages[i].Role {
			reason := ""
			if mediaMismatch {
				reason = " (media mismatch)"
			}
			return nil, fmt.Errorf(
				"message history diverges at index %d%s:\n%s",
				i, reason,
				cmp.Diff(recordingDiffMessage(c.messages[i]), recordingDiffMessage(message)),
			)
		}
	}
	msg := c.messages[len(history)]

	// Build the same per-block display spans a streaming provider builds, so a
	// recording-backed turn produces telemetry identical to a live one:
	// thinking, text response, and one span per tool call carrying the
	// Boundary/roll-up attributes the evaluation loop needs to nest the tool's
	// execution beneath its own call. Without these, every recording-backed
	// tool call would run under the shared loop context and every
	// recording-driven test would exercise a shape production never has.
	//
	// Note this is the *live loop* path (model `recording/…`), which is distinct
	// from LLM.EmitHistory: that one re-emits spans for an already-recorded
	// conversation for display only, and never runs tools. The two never both
	// emit spans for the same tool call.
	var callDigest string
	if opts != nil {
		callDigest = opts.CallDigest
	}
	dp := newDisplayPhases(ctx, callDigest, tools)
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
