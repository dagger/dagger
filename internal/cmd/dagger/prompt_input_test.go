package daggercmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/stretchr/testify/require"
)

type fakePromptRuntime struct {
	*fakeRuntime
	prompts chan idtui.PromptInput
	sendErr error
}

func newFakePromptRuntime() *fakePromptRuntime {
	return &fakePromptRuntime{fakeRuntime: newFakeRuntime(), prompts: make(chan idtui.PromptInput, 8)}
}

func (f *fakePromptRuntime) SendPrompt(_ context.Context, input idtui.PromptInput) (agentMessage, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.prompts <- input
	return fakeMessage{}, nil
}

func (f *fakePromptRuntime) awaitPrompt(t *testing.T) idtui.PromptInput {
	t.Helper()
	select {
	case input := <-f.prompts:
		return input
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for prompt")
		return idtui.PromptInput{}
	}
}

func imagePrompt(text string) idtui.PromptInput {
	return idtui.PromptInput{Text: text, Images: []idtui.PromptImage{{MIMEType: "image/png", Data: []byte("private-image-payload")}}}
}

func TestPromptQueueRecallAndReplace(t *testing.T) {
	h := &shellCallHandler{}
	original := imagePrompt("describe this")
	h.QueuePrompt(original)
	original.Images[0].Data[0] = '!'
	recalled := h.DequeuePrompt()
	require.Equal(t, imagePrompt("describe this"), recalled)
	require.True(t, h.DequeuePrompt().Empty())

	recalled.Text = "edited caption"
	h.QueuePrompt(recalled)
	recalled.Images[0].Data[0] = '!'
	require.Equal(t, imagePrompt("edited caption"), h.DequeuePrompt())

	h.QueuePrompt(imagePrompt("replaced"))
	h.QueuePrompt(imagePrompt(""))
	require.Equal(t, imagePrompt(""), h.DequeuePrompt(), "image-only drafts are not empty")

	h.QueuePrompt(imagePrompt("replaced by text"))
	h.QueueMessage("legacy text")
	require.Equal(t, idtui.PromptInput{Text: "legacy text"}, h.DequeuePrompt())
	h.QueueMessage("old caller")
	require.Equal(t, "old caller", h.DequeueMessage())
}

func TestPromptAttachmentsRejectCommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode interpreterMode
		text string
	}{
		{"shell", modeShell, "echo must-not-run"},
		{"shell image only", modeShell, ""},
		{"unset", modeUnset, ""},
		{"exit", modePrompt, "exit"},
		{"slash exit", modePrompt, " /exit "},
		{"slash builtin", modePrompt, "/clear"},
		{"unknown slash", modePrompt, "/unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, agents := testSession(t, "chief")
			agents[0].beginTurn(func(error) {})
			defer agents[0].endTurn()
			cancelled := false
			h := &shellCallHandler{mode: tc.mode, llmSession: s, cancel: func() { cancelled = true }}
			input := imagePrompt(tc.text)
			require.False(t, h.SubmitPromptToTarget(input))
			err := h.HandlePrompt(t.Context(), input)
			require.Error(t, err)
			require.NotContains(t, err.Error(), string(input.Images[0].Data))
			require.False(t, cancelled, "attachments must be rejected before exit")
			runtimeOf(t, agents[0]).awaitNoSend(t)
		})
	}
}

func TestPromptImagesFollowFocusMidTurn(t *testing.T) {
	s, agents := testSession(t, "chief", "scout")
	chief, scout := agents[0], agents[1]
	rt := newFakePromptRuntime()
	// An attached worker is somebody else's runtime, but normal mailbox sends
	// are permitted. Sending media must not reseed or replace it.
	scout.bindRuntime(rt, "agent-scout", "attached-id", false)
	scout.beginTurn(func(error) {})
	defer scout.endTurn()
	h := &shellCallHandler{mode: modePrompt, llmSession: s}
	require.False(t, h.SubmitPromptToTarget(imagePrompt("for chief")))
	require.Empty(t, rt.prompts)

	s.SetTarget(scout)
	input := imagePrompt("for scout")
	require.True(t, h.SubmitPromptToTarget(input))
	input.Images[0].Data[0] = '!'
	require.Equal(t, imagePrompt("for scout"), rt.awaitPrompt(t))
	require.True(t, h.SubmitPromptToTarget(imagePrompt("")))
	require.Equal(t, imagePrompt(""), rt.awaitPrompt(t))
	require.Zero(t, rt.reseedCount())
	require.Same(t, rt, scout.runtime())
	runtimeOf(t, chief).awaitNoSend(t)
}

func TestPromptImagesPendingWhileSpawning(t *testing.T) {
	s, _ := testSession(t)
	a := s.newAgent("fresh")
	s.SetTarget(a)
	a.beginTurn(func(error) {})
	defer a.endTurn()
	input := imagePrompt("second")
	require.True(t, s.SubmitPromptToTarget(input))
	input.Images[0].Data[0] = '!'
	require.True(t, s.SubmitPromptToTarget(imagePrompt("")))
	rt := newFakePromptRuntime()
	a.bindRuntime(rt, "fresh", "", true)
	_, err := sendAgentPrompt(t.Context(), rt, imagePrompt("first"))
	require.NoError(t, err)
	a.flushPending(rt)
	require.Equal(t, imagePrompt("first"), rt.awaitPrompt(t))
	require.Equal(t, imagePrompt("second"), rt.awaitPrompt(t))
	require.Equal(t, imagePrompt(""), rt.awaitPrompt(t))
	require.Empty(t, a.pending)
}

func TestPromptImageOnlyInitialTurn(t *testing.T) {
	// Model reads are the only engine plumbing needed when a runtime is already
	// attached. Exercise the real HandlePrompt -> WithPromptInput idle path.
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
		var query dagger.Request
		require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
		require.Contains(t, query.Query, "model")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"node":{"model":"test-model"}}}`))}, nil
	}}))
	require.NoError(t, err)
	defer dag.Close()
	s, agents := testSession(t, "chief")
	s.dag = dag
	a := agents[0]
	a.autoCompact = false
	a.llm = dagger.Ref[*dagger.LLM](dag, "seed")
	rt := newFakePromptRuntime()
	rt.snapshot = "snapshot"
	a.bindRuntime(rt, "chief", "attached-id", false)
	h := &shellCallHandler{mode: modePrompt, llmSession: s}
	require.NoError(t, h.HandlePrompt(t.Context(), imagePrompt("")))
	require.Equal(t, imagePrompt(""), rt.awaitPrompt(t))
	require.Equal(t, "[1 image(s)]", h.initialPrompt)
	require.Equal(t, 1, rt.resumes)
	require.Zero(t, rt.reseedCount())
	require.Nil(t, a.turnCancel)
}

func TestPromptImageOnlySpawnsThroughMailbox(t *testing.T) {
	input := imagePrompt("")
	var calls []string
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
		var query dagger.Request
		require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
		var node map[string]any
		switch {
		case strings.Contains(query.Query, "spawn("):
			calls = append(calls, "spawn")
			node = map[string]any{"spawn": "new-agent"}
		case strings.Contains(query.Query, "send("):
			calls = append(calls, "send")
			require.Contains(t, query.Query, `message:""`)
			require.Contains(t, query.Query, base64.StdEncoding.EncodeToString(input.Images[0].Data))
			node = map[string]any{"send": "new-message"}
		case strings.Contains(query.Query, "handle"):
			node = map[string]any{"handle": "fresh-handle"}
		case strings.Contains(query.Query, "resume"):
			calls = append(calls, "resume")
			node = map[string]any{"resume": "new-agent"}
		case strings.Contains(query.Query, "response"):
			calls = append(calls, "response")
			node = map[string]any{"response": "image understood"}
		case strings.Contains(query.Query, "snapshot"):
			node = map[string]any{"snapshot": map[string]string{"id": "new-snapshot"}}
		default:
			require.Contains(t, query.Query, "model")
			node = map[string]any{"model": "test-model"}
		}
		body, err := json.Marshal(map[string]any{"data": map[string]any{"node": node}})
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}}))
	require.NoError(t, err)
	defer dag.Close()
	s, _ := testSession(t)
	s.dag = dag
	a := s.newAgent("fresh")
	a.autoCompact = false
	a.llm = dagger.Ref[*dagger.LLM](dag, "seed")
	s.SetTarget(a)
	h := &shellCallHandler{mode: modePrompt, llmSession: s}
	require.NoError(t, h.HandlePrompt(t.Context(), input))
	require.Equal(t, []string{"spawn", "send", "resume", "response"}, calls)
	require.Equal(t, "fresh-handle", a.agentHandle)
	require.True(t, a.owned)
}

func TestPromptImagesUseLiveAgentMailbox(t *testing.T) {
	for _, text := range []string{"", "caption"} {
		t.Run("text="+text, func(t *testing.T) {
			input := imagePrompt(text)
			var sent bool
			dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
				var query dagger.Request
				require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
				require.Contains(t, query.Query, "send(")
				require.Contains(t, query.Query, `message:"`+text+`"`)
				require.Contains(t, query.Query, "content:")
				require.Contains(t, query.Query, base64.StdEncoding.EncodeToString(input.Images[0].Data))
				require.NotContains(t, query.Query, "reseed")
				sent = true
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"node":{"send":"message-id"}}}`))}, nil
			}}))
			require.NoError(t, err)
			defer dag.Close()
			rt := liveAgent{dag: dag, agent: dagger.Ref[*dagger.Agent](dag, "existing-agent")}
			_, err = sendAgentPrompt(t.Context(), rt, input)
			require.NoError(t, err)
			require.True(t, sent)
		})
	}
}

func TestPromptImageBlocks(t *testing.T) {
	input := imagePrompt("caption remains message text")
	input.Images = append(input.Images, idtui.PromptImage{MIMEType: "image/jpeg", Data: []byte{0, 1, 2, 255}})
	blocks := promptImageBlocks(input)
	require.Len(t, blocks, 2)
	for i, block := range blocks {
		require.Equal(t, dagger.LLMContentBlockKindImage, block.Kind)
		require.Equal(t, input.Images[i].MIMEType, block.MimeType)
		decoded, err := base64.StdEncoding.DecodeString(block.Data)
		require.NoError(t, err)
		require.Equal(t, input.Images[i].Data, decoded)
		require.Empty(t, block.Text)
		require.Nil(t, block.File)
	}
}

func TestPromptImageDiagnosticsHidePayload(t *testing.T) {
	input := imagePrompt("caption")
	encoded := base64.StdEncoding.EncodeToString(input.Images[0].Data)
	cause := errors.New("send failed with " + string(input.Images[0].Data) + " and " + encoded)
	rt := newFakePromptRuntime()
	rt.sendErr = cause
	_, err := sendAgentPrompt(t.Context(), rt, input)
	require.ErrorIs(t, err, cause)
	require.NotContains(t, err.Error(), string(input.Images[0].Data))
	require.NotContains(t, err.Error(), encoded)

	_, err = sendAgentPrompt(t.Context(), newFakeRuntime(), input)
	require.ErrorContains(t, err, "does not support image attachments")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	s, _ := testSession(t)
	a := s.newAgent("failed spawn")
	a.beginTurn(func(error) {})
	require.True(t, a.SubmitPrompt(input))
	a.endTurn()
	a.sendPrompt(rt, input)
	require.Contains(t, logs.String(), "caption [1 image(s)]")
	require.NotContains(t, logs.String(), string(input.Images[0].Data))
	require.NotContains(t, logs.String(), encoded)
}
