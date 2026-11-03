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

	// SubmissionMode is opaque handler-owned input-mode metadata. Both prompts
	// and commands retain their interpretation while waiting in a queue.
	SubmissionMode any

	// CommandTarget is opaque handler-owned submission metadata. It binds a
	// command to its original target while asynchronous handling or a queue
	// waits; ordinary user prompts leave it nil.
	CommandTarget any
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
	clone := PromptInput{Text: p.Text, SubmissionMode: p.SubmissionMode, CommandTarget: p.CommandTarget}
	for _, image := range p.Images {
		clone.Images = append(clone.Images, PromptImage{
			MIMEType: image.MIMEType,
			Data:     append([]byte(nil), image.Data...),
		})
	}
	return clone
}

// PromptPreparer captures handler-owned command routing before submission
// crosses a goroutine or queue boundary. It must not perform blocking work.
type PromptPreparer interface {
	PreparePrompt(PromptInput) PromptInput
}

// PromptQueueControl separates committed commands from the recallable text
// draft. Cancellation discards both; recall only withdraws the draft.
type PromptQueueControl interface {
	RecallQueuedPrompt() PromptInput
	ClearQueuedPrompts()
	QueuedPromptSummary() string
}

// PromptInputHandler is the optional multimodal extension of ShellHandler.
// Text-only shell implementations continue to use the original methods.
type PromptInputHandler interface {
	HandlePrompt(context.Context, PromptInput) error
	SubmitPromptToTarget(PromptInput) bool
	QueuePrompt(PromptInput)
	DequeuePrompt() PromptInput
}
