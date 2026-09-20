package idtui

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

func TestPromptImageCancelAbandonsHistoryDraft(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	fe.inputHistory = []string{"old prompt"}
	fe.textInput.SetValue("canceled draft")
	require.NoError(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte("private")}))
	require.True(t, fe.historyUp())

	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	fe.tui.Step()
	require.Equal(t, -1, fe.historyIndex)
	require.Empty(t, fe.historySaved)
	require.Empty(t, fe.historyImages)
	require.False(t, fe.historyDown(), "canceled draft must not be resurrected")
	require.Empty(t, fe.textInput.Value())
	require.Empty(t, fe.promptImages)
}

func TestPromptImageCancelClearsLoadingError(t *testing.T) {
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
	fe.tui.Inject(tuist.ParseKey("enter"))
	fe.tui.Step()
	require.ErrorContains(t, fe.promptErr, "finish loading")

	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	fe.tui.Step()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("clipboard reader was not canceled")
	}
	require.False(t, fe.imagePasting)
	require.NoError(t, fe.promptErr, "a canceled read must not tell the user to keep waiting")
}

func TestPromptImageRestoredDraftReleasesParkedPayload(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	image := PromptImage{MIMEType: "image/png", Data: []byte("private")}
	fe.agentDrafts = map[string]PromptInput{"agent": {Text: "draft", Images: []PromptImage{image}}}
	fe.restoreAgentDraft("agent")
	require.Equal(t, []PromptImage{image}, fe.promptImages)
	require.NotContains(t, fe.agentDrafts, "agent", "active draft must not retain another parked copy of its payload")
	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	fe.tui.Step()
	require.Empty(t, fe.promptImages)
	require.Empty(t, fe.agentDrafts)
}

func TestPromptImageFocusPreservesImagesAddedToHistory(t *testing.T) {
	fe, _ := imagePromptFrontend(t)
	fe.inputHistory = []string{"recalled prompt"}
	fe.textInput.SetValue("original draft")
	require.NoError(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte("original image")}))
	require.True(t, fe.historyUp())
	image := PromptImage{MIMEType: "image/png", Data: []byte("new image")}
	require.NoError(t, fe.appendPromptImage(image))

	fe.saveDraftFor("first")
	fe.restoreAgentDraft("second")
	fe.restoreAgentDraft("first")
	require.Equal(t, "recalled prompt", fe.textInput.Value())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
}

func TestPromptImageRecallAbandonsHistoryDraft(t *testing.T) {
	fe, handler := imagePromptFrontend(t)
	fe.inputHistory = []string{"old prompt"}
	fe.textInput.SetValue("abandoned draft")
	require.NoError(t, fe.appendPromptImage(PromptImage{MIMEType: "image/png", Data: []byte("old image")}))
	require.True(t, fe.historyUp())
	queued := PromptInput{Text: "queued", Images: []PromptImage{{MIMEType: "image/png", Data: []byte("queued image")}}}
	fe.setQueuedPrompt(queued)

	fe.tui.Inject(tuist.ParseKey("alt+up"))
	fe.tui.Step()
	require.True(t, handler.queued.Empty())
	require.Equal(t, queued.Text, fe.textInput.Value())
	require.Equal(t, queued.Images, fe.promptImages)
	require.Empty(t, fe.historyImages)
	require.Empty(t, fe.historySaved)
	require.False(t, fe.historyDown(), "history navigation must not overwrite the recalled draft")
	require.Equal(t, queued.Images, fe.promptImages)
}

func TestPromptImageRecallDoesNotFabricateLabelInput(t *testing.T) {
	fe, handler := imagePromptFrontend(t)
	image := PromptImage{MIMEType: "image/png", Data: []byte("current image")}
	fe.textInput.SetValue("current draft")
	require.NoError(t, fe.appendPromptImage(image))
	fe.queuedMsgLabel.SetMessage("already consumed [1 image(s)]")
	require.True(t, handler.queued.Empty())

	fe.tui.Inject(tuist.ParseKey("alt+up"))
	fe.tui.Step()
	require.Equal(t, "current draft", fe.textInput.Value())
	require.Equal(t, []PromptImage{image}, fe.promptImages)
	require.Empty(t, fe.queuedMsgLabel.Message())
}
