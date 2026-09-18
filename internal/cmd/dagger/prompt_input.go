package daggercmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/dagql/idtui"
)

// promptAgentRuntime extends the text-only runtime contract without requiring
// old runtime implementations to pretend they support attachments.
type promptAgentRuntime interface {
	SendPrompt(context.Context, idtui.PromptInput) (agentMessage, error)
}

func (l liveAgent) SendPrompt(ctx context.Context, input idtui.PromptInput) (agentMessage, error) {
	return l.agent.Send(ctx, input.Text, core.AgentSendOpts{Content: promptImageBlocks(input)})
}

func promptImageBlocks(input idtui.PromptInput) []core.LLMContentBlockInput {
	var blocks []core.LLMContentBlockInput
	for _, image := range input.Images {
		blocks = append(blocks, core.LLMContentBlockInput{
			Kind:     core.LLMContentBlockKindImage,
			MimeType: image.MIMEType,
			Data:     base64.StdEncoding.EncodeToString(image.Data),
		})
	}
	return blocks
}

// sendAgentPrompt always uses the mailbox. Attaching media must not replace the
// LLM seed or reseed an attached runtime, and must retain normal send origin.
func sendAgentPrompt(ctx context.Context, rt agentRuntime, input idtui.PromptInput) (agentMessage, error) {
	if len(input.Images) == 0 {
		return rt.SendMessage(ctx, input.Text)
	}
	mediaRT, ok := rt.(promptAgentRuntime)
	if !ok {
		return nil, fmt.Errorf("agent runtime does not support image attachments")
	}
	msg, err := mediaRT.SendPrompt(ctx, input)
	if err != nil {
		return nil, promptDiagnosticError(err, input)
	}
	return promptMessage{agentMessage: msg, input: input}, nil
}

// Errors from a transport can include the submitted GraphQL arguments. Keep
// image payloads out of diagnostics while preserving error identity for callers.
func promptDiagnosticError(err error, input idtui.PromptInput) error {
	if err == nil || len(input.Images) == 0 {
		return err
	}
	text := err.Error()
	for _, image := range input.Images {
		if len(image.Data) != 0 {
			text = strings.ReplaceAll(text, base64.StdEncoding.EncodeToString(image.Data), "[image data]")
			text = strings.ReplaceAll(text, string(image.Data), "[image data]")
		}
	}
	return promptError{cause: err, text: text}
}

type promptError struct {
	cause error
	text  string
}

func (e promptError) Error() string { return e.text }
func (e promptError) Unwrap() error { return e.cause }

type promptMessage struct {
	agentMessage
	input idtui.PromptInput
}

func (m promptMessage) Delivery(ctx context.Context) (core.AgentMessageDelivery, error) {
	delivery, err := m.agentMessage.Delivery(ctx)
	return delivery, promptDiagnosticError(err, m.input)
}

func (m promptMessage) Response(ctx context.Context) (string, error) {
	response, err := m.agentMessage.Response(ctx)
	return response, promptDiagnosticError(err, m.input)
}
