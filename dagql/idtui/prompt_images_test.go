package idtui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

type imagePromptHandler struct {
	stubShellHandler
	mode    bool
	absorb  bool
	sent    []PromptInput
	queued  PromptInput
	handled chan PromptInput
}

func (h *imagePromptHandler) PromptMode() bool   { return h.mode }
func (h *imagePromptHandler) Serial(string) bool { return false }
func (h *imagePromptHandler) HandlePrompt(_ context.Context, input PromptInput) error {
	h.handled <- input
	return nil
}
func (h *imagePromptHandler) SubmitPromptToTarget(input PromptInput) bool {
	if !h.absorb {
		return false
	}
	h.sent = append(h.sent, input.Clone())
	return true
}
func (h *imagePromptHandler) QueuePrompt(input PromptInput) { h.queued = input.Clone() }
func (h *imagePromptHandler) DequeuePrompt() PromptInput {
	input := h.queued
	h.queued = PromptInput{}
	return input
}

func imagePromptFrontend(t *testing.T) (*frontendPretty, *imagePromptHandler) {
	t.Helper()
	handler := &imagePromptHandler{mode: true, handled: make(chan PromptInput, 4)}
	fe := newHistoryTestFrontend(t, handler)
	fe.inputHistory = nil
	return fe, handler
}

// commandPromptHandler exercises the optional routing/queue hooks without an
// engine. The command policy itself is covered by daggercmd's handler tests.
type commandPromptHandler struct {
	imagePromptHandler
	target   string
	prepared int
	commands []PromptInput
}

func (h *commandPromptHandler) ValidatePrompt(input PromptInput) error {
	if input.Text == "/unknown" {
		return errors.New("unknown agent command")
	}
	return nil
}

func (h *commandPromptHandler) PreparePrompt(input PromptInput) PromptInput {
	h.prepared++
	if strings.HasPrefix(input.Text, "/") {
		input.CommandTarget = h.target
	}
	return input
}

func (h *commandPromptHandler) SerialPrompt(input PromptInput) bool {
	return input.CommandTarget != nil
}

func (h *commandPromptHandler) SubmitPromptToTarget(input PromptInput) bool {
	return !h.SerialPrompt(input) && h.imagePromptHandler.SubmitPromptToTarget(input)
}

func (h *commandPromptHandler) QueuePrompt(input PromptInput) {
	if h.SerialPrompt(input) {
		h.commands = append(h.commands, input.Clone())
	} else {
		h.queued = input.Clone()
	}
}

func (h *commandPromptHandler) DequeuePrompt() PromptInput {
	if len(h.commands) > 0 {
		input := h.commands[0]
		h.commands = h.commands[1:]
		return input
	}
	return h.imagePromptHandler.DequeuePrompt()
}

func (h *commandPromptHandler) RecallQueuedPrompt() PromptInput {
	return h.imagePromptHandler.DequeuePrompt()
}

func (h *commandPromptHandler) ClearQueuedPrompts() {
	h.commands = nil
	h.queued = PromptInput{}
}

func (h *commandPromptHandler) QueuedPromptSummary() string {
	if len(h.commands) == 0 {
		return h.queued.Summary()
	}
	return fmt.Sprintf("%d commands queued; %s", len(h.commands), h.queued.Summary())
}

func commandPromptFrontend(t *testing.T) (*frontendPretty, *commandPromptHandler) {
	t.Helper()
	h := &commandPromptHandler{
		imagePromptHandler: imagePromptHandler{mode: true, handled: make(chan PromptInput, 8)},
		target:             "chief",
	}
	fe := newHistoryTestFrontend(t, h)
	fe.inputHistory = nil
	return fe, h
}

func TestPromptCommandValidationRetainsDraft(t *testing.T) {
	for _, busy := range []bool{false, true} {
		fe, handler := commandPromptFrontend(t)
		handler.absorb = busy
		fe.textInput.SetValue("/unknown")
		fe.tui.Inject(tuist.ParseKey("enter"))
		fe.tui.Step()
		// The widget clears accepted input after OnSubmit returns. Assert only
		// after the real key path completes, not inside the submit callback.
		require.ErrorContains(t, fe.promptErr, "unknown agent command")
		require.Equal(t, "/unknown", fe.textInput.Value())
		require.Empty(t, fe.inputHistory)
		require.Empty(t, handler.sent)
		require.Empty(t, handler.handled)
		require.Zero(t, handler.prepared, "invalid input is not a submitted command")
	}
}

func TestPromptCommandRunsAlongsideAgent(t *testing.T) {
	fe, handler := commandPromptFrontend(t)
	handler.absorb = true
	fe.turnsRunning = 1 // an agent turn, not the serial interpreter
	interrupted := false
	fe.shellInterrupt = func(error) { interrupted = true }
	fe.textInput.SetValue("/help")
	fe.handleInputComplete()
	select {
	case input := <-handler.handled:
		require.Equal(t, "/help", input.Text)
		require.Equal(t, "chief", input.CommandTarget)
	case <-time.After(time.Second):
		t.Fatal("read-only command waited for the agent turn")
	}
	require.Empty(t, handler.sent, "a command is not an interjection")
	require.False(t, interrupted)
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return fe.turnsRunning == 1 && !fe.serialRunning
	}, time.Second, time.Millisecond)
}

func TestPromptCommandQueueFIFOAndSubmissionTarget(t *testing.T) {
	fe, handler := commandPromptFrontend(t)
	fe.serialRunning = true
	fe.turnsRunning = 1
	for _, text := range []string{"/model first", "old draft", "/effort high", "latest draft"} {
		fe.textInput.SetValue(text)
		fe.handleInputComplete()
	}
	require.Len(t, handler.commands, 2)
	require.Equal(t, "latest draft", handler.queued.Text)
	require.Contains(t, fe.queuedMsgLabel.Message(), "2 commands")
	handler.target = "scout"
	fe.handleShellDone(nil, true)
	for _, text := range []string{"/model first", "/effort high", "latest draft"} {
		var got PromptInput
		require.Eventually(t, func() bool {
			fe.tui.Step()
			select {
			case got = <-handler.handled:
				return true
			default:
				return false
			}
		}, time.Second, time.Millisecond)
		require.Equal(t, text, got.Text)
		if strings.HasPrefix(text, "/") {
			require.Equal(t, "chief", got.CommandTarget)
		} else {
			require.Nil(t, got.CommandTarget, "normal messages do not pin a command target")
		}
	}
	require.Equal(t, 4, handler.prepared, "queued execution must not prepare or retarget again")
}

func TestPromptCommandQueueRecallAndCancellation(t *testing.T) {
	fe, handler := commandPromptFrontend(t)
	fe.serialRunning = true
	for _, text := range []string{"/model first", "draft", "/compact"} {
		fe.textInput.SetValue(text)
		fe.handleInputComplete()
	}
	require.True(t, fe.recallQueuedPrompt())
	require.Equal(t, "draft", fe.textInput.Value())
	require.Len(t, handler.commands, 2, "recall must not withdraw a submitted command")
	require.True(t, handler.queued.Empty())
	require.Contains(t, fe.queuedMsgLabel.Message(), "2 commands")
	fe.interruptCurrent()
	require.Empty(t, handler.commands)
	require.True(t, handler.queued.Empty())
	require.Empty(t, fe.queuedMsgLabel.Message())
}

func TestPromptImagePasteAndSubmit(t *testing.T) {
	fe, handler := imagePromptFrontend(t)
	handler.absorb = true
	image := PromptImage{MIMEType: "image/png", Data: []byte("private image bytes")}
	started, release := make(chan struct{}), make(chan struct{})
	fe.clipboardImage = func(context.Context) (PromptImage, error) {
		close(started)
		<-release
		return image, nil
	}
	fe.textInput.SetValue("look at this")
	fe.tui.Inject(tuist.ParseKey("ctrl+v"))
	frame := strings.Join(fe.tui.Step(), "\n")
	<-started
	require.Contains(t, frame, "Reading clipboard image")
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	require.Equal(t, "look at this", fe.textInput.Value(), "submit must wait for clipboard read")
	require.Empty(t, handler.sent)
	close(release)
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return !fe.imagePasting
	}, time.Second, time.Millisecond)
	frame = strings.Join(fe.tui.Step(), "\n")
	require.Contains(t, frame, "[image 1: image/png, 1 KiB]")
	require.Contains(t, navKeyHelp(fe.keys(NewOutput(new(strings.Builder)))), "paste image")
	require.NotContains(t, frame, string(image.Data))
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	require.Len(t, handler.sent, 1)
	require.Equal(t, "look at this", handler.sent[0].Text)
	require.Equal(t, []PromptImage{image}, handler.sent[0].Images)
	require.Empty(t, fe.promptImages)
	require.Empty(t, fe.textInput.Value())
	require.Equal(t, []string{"look at this"}, fe.inputHistory)
	require.Contains(t, fe.queuedMsgLabel.Message(), "1 image")
	require.NotContains(t, fe.queuedMsgLabel.Message(), string(image.Data))
}

func TestPromptImageOnlyAndQueueRecall(t *testing.T) {
	fe, handler := imagePromptFrontend(t)
	image := PromptImage{MIMEType: "image/png", Data: []byte("image")}
	require.NoError(t, fe.appendPromptImage(image))
	fe.serialRunning = true
	fe.syncPrompt()
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	require.Equal(t, []PromptImage{image}, handler.queued.Images)
	require.Empty(t, fe.promptImages)
	require.Empty(t, fe.inputHistory, "image-only submissions must not write payloads or empty entries to text history")
	fe.tui.Inject(tuist.ParseKey("alt+up"))
	fe.tui.Step()
	require.True(t, handler.queued.Empty())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
	require.Empty(t, fe.textInput.Value())
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	fe.handleShellDone(nil, true)
	select {
	case input := <-handler.handled:
		require.Empty(t, input.Text)
		require.Equal(t, []PromptImage{image}, input.Images)
	case <-time.After(time.Second):
		t.Fatal("image-only queued prompt was not delivered")
	}
}

func TestPromptImageRejectionAndRemoval(t *testing.T) {
	for _, text := range []string{"/clear", "exit", "/unknown-command"} {
		t.Run(text, func(t *testing.T) {
			fe, handler := imagePromptFrontend(t)
			handler.absorb = true
			image := PromptImage{MIMEType: "image/png", Data: []byte("image")}
			require.NoError(t, fe.appendPromptImage(image))
			fe.textInput.SetValue(text)
			fe.tui.Inject(tuist.ParseKey("enter"))
			fe.tui.Step()
			require.ErrorContains(t, fe.promptErr, "remove image attachments")
			require.Equal(t, text, fe.textInput.Value())
			require.Equal(t, []PromptImage{image}, fe.promptImages)
			require.Empty(t, handler.sent)
		})
	}
	fe, handler := imagePromptFrontend(t)
	require.NoError(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte("image")}))
	handler.mode = false
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	require.ErrorContains(t, fe.promptErr, "prompt mode")
	require.Len(t, fe.promptImages, 1)
	fe.tui.Inject(tuist.ParseKey("backspace"))
	fe.tui.Step()
	require.Empty(t, fe.promptImages)
}

func TestPromptImageDraftsAndHistory(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	image := PromptImage{MIMEType: "image/png", Data: []byte("private")}
	require.NoError(t, fe.appendPromptImage(image))
	fe.textInput.SetValue("draft")
	fe.saveDraftFor("first")
	fe.restoreAgentDraft("second")
	require.Empty(t, fe.promptImages)
	fe.restoreAgentDraft("first")
	require.Equal(t, "draft", fe.textInput.Value())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
	fe.inputHistory = []string{"old text"}
	require.True(t, fe.historyUp())
	require.Empty(t, fe.promptImages)
	require.True(t, fe.historyDown())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
	require.Equal(t, "draft", fe.textInput.Value())
	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	fe.tui.Step()
	require.Empty(t, fe.promptImages)
	require.Empty(t, fe.textInput.Value())
}

func TestPromptImageCanceledPaste(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	started, finished := make(chan struct{}), make(chan struct{})
	fe.clipboardImage = func(ctx context.Context) (PromptImage, error) {
		close(started)
		<-ctx.Done()
		defer close(finished)
		return PromptImage{}, ctx.Err()
	}
	fe.tui.Inject(tuist.ParseKey("ctrl+v"))
	fe.tui.Step()
	<-started
	fe.restoreAgentDraft("other-agent")
	<-finished
	fe.tui.Step()
	require.False(t, fe.imagePasting)
	require.Empty(t, fe.promptImages)
	require.NoError(t, fe.promptErr)
}

func TestPromptImageClipboardFailurePreservesDraft(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	fe.textInput.SetValue("draft")
	image := PromptImage{MIMEType: "image/png", Data: []byte("image")}
	require.NoError(t, fe.appendPromptImage(image))
	fe.clipboardImage = func(context.Context) (PromptImage, error) { return PromptImage{}, ErrClipboardNoImage }
	fe.tui.Inject(tuist.ParseKey("ctrl+v"))
	fe.tui.Step()
	require.Eventually(t, func() bool { fe.tui.Step(); return !fe.imagePasting }, time.Second, time.Millisecond)
	require.True(t, errors.Is(fe.promptErr, ErrClipboardNoImage))
	require.Equal(t, "draft", fe.textInput.Value())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
}

func TestPromptImageLimits(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	for range maxPromptImages {
		require.NoError(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte{1}}))
	}
	require.ErrorContains(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte{1}}), "at most")
	fe.promptImages = []PromptImage{{MIMEType: "image/png", Data: make([]byte, maxPromptImageBytes)}}
	require.ErrorContains(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte{1}}), "20 MiB")
}
