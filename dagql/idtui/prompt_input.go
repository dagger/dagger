package idtui

import (
	"context"
	"fmt"
	"strings"
)

// PromptImage is an explicitly attached local image. Data is never placed in
// text history, log messages, or queue labels.
type PromptImage struct {
	MIMEType string
	Data     []byte
}

// PromptInput keeps attachments with the text through queuing and delivery.
// Callers must not mutate image data after submitting a prompt.
type PromptInput struct {
	Text   string
	Images []PromptImage
}

func (p PromptInput) Empty() bool {
	return strings.TrimSpace(p.Text) == "" && len(p.Images) == 0
}

// Summary is a payload-free representation for queue labels and diagnostics.
func (p PromptInput) Summary() string {
	if len(p.Images) == 0 {
		return p.Text
	}
	label := fmt.Sprintf("[%d image(s)]", len(p.Images))
	if p.Text == "" {
		return label
	}
	return p.Text + " " + label
}

// Clone isolates a draft from asynchronous delivery and queue storage.
func (p PromptInput) Clone() PromptInput {
	clone := PromptInput{Text: p.Text}
	for _, image := range p.Images {
		clone.Images = append(clone.Images, PromptImage{
			MIMEType: image.MIMEType,
			Data:     append([]byte(nil), image.Data...),
		})
	}
	return clone
}

// PromptInputHandler is the optional multimodal extension of ShellHandler.
// Text-only shell implementations continue to use the original methods.
type PromptInputHandler interface {
	HandlePrompt(context.Context, PromptInput) error
	SubmitPromptToTarget(PromptInput) bool
	QueuePrompt(PromptInput)
	DequeuePrompt() PromptInput
}
