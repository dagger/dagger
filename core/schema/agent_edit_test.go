package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type agentEditSchemaServer struct {
	*agentIdentityTestServer
	conn *grpc.ClientConn
}

func (*agentEditSchemaServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return dagql.ObjectResult[*core.Module]{}, core.ErrNoCurrentModule
}

func (s *agentEditSchemaServer) NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error) {
	return s.mainClient, nil
}

func (s *agentEditSchemaServer) SpecificClientAttachableConn(context.Context, string, core.SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	return s.conn, true, nil
}

func TestAgentBoundaryEditPreservesResumeInput(t *testing.T) {
	for _, mode := range []string{"failed", "paused", "never-started", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			cache, err := dagql.NewCache(t.Context(), "", nil, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
			ctx, srv, agents := agentIdentitySession(t, cache, "edit-resume-"+mode)
			query, err := core.CurrentQuery(ctx)
			require.NoError(t, err)
			old := query.Server.(*agentIdentityTestServer)
			env := &llmAuthSecrets{vars: map[string]string{"env://ANTHROPIC_API_KEY": "key"}}
			query.Server = &agentEditSchemaServer{agentIdentityTestServer: old, conn: newLLMAuthAttachable(t, env)}
			var requests atomic.Int64
			var requestedModel atomic.Value
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				requestedModel.Store(request.Model)
				n := requests.Add(1)
				if mode == "failed" && n == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"retry after changing effort"}}`))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_retry\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"resumed\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			}))
			t.Cleanup(provider.Close)
			old.mainClient.LLMConfig = &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
				"anthropic": {APIKey: "env://ANTHROPIC_API_KEY", BaseURL: provider.URL},
			}}
			var base dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, srv.Root(), &base,
				dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("claude-sonnet-4-5"))}}},
				dagql.Selector{Field: "withPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("pending request")}}}))
			agent, err := spawnIdentityAgent(ctx, srv, base)
			require.NoError(t, err)
			rt, err := agents.Require(ctx, agent)
			require.NoError(t, err)
			if mode == "failed" {
				require.NoError(t, rt.Resume(ctx))
				state, err := rt.WaitSettled(ctx)
				require.NoError(t, err)
				require.Equal(t, core.AgentStateFailed, state)
				require.EqualValues(t, 1, requests.Load())
			} else if mode == "paused" {
				require.NoError(t, rt.Pause())
			} else if mode == "stopped" {
				require.NoError(t, rt.Stop(ctx, true, nil, core.AgentStopExplicit))
			}
			before := rt.State()
			var edited dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, agent, &edited,
				dagql.Selector{Field: "__beginEdit", Args: []dagql.NamedInput{{Name: "token", Value: dagql.String("edit")}}},
				dagql.Selector{Field: "withReasoningEffort", Args: []dagql.NamedInput{{Name: "effort", Value: dagql.String("low")}}}))
			t.Cleanup(func() { rt.AbortConversationEdit("edit") })
			if mode == "stopped" {
				require.NoError(t, srv.Select(ctx, edited, &edited, dagql.Selector{Field: "withModel", Args: []dagql.NamedInput{{Name: "model", Value: dagql.String("claude-opus-4-6")}}}))
			}
			rawID, err := edited.ID()
			require.NoError(t, err)
			var id core.AgentID
			require.NoError(t, srv.Select(ctx, agent, &id, dagql.Selector{Field: "__commitEdit", Args: []dagql.NamedInput{
				{Name: "token", Value: dagql.String("edit")}, {Name: "conversation", Value: dagql.NewID[*core.LLM](rawID)},
			}}))
			require.Equal(t, before, rt.State(), "commit must not restart the loop")
			if mode == "stopped" {
				require.Zero(t, requests.Load())
				message, err := agents.Send(ctx, agent, "continue after setup", "")
				require.NoError(t, err)
				_, err = agents.MessageResponse(ctx, message)
				require.NoError(t, err)
				require.Equal(t, "claude-opus-4-6", requestedModel.Load())
			} else {
				require.NoError(t, rt.Resume(ctx))
			}
			_, err = rt.WaitSettled(ctx)
			require.NoError(t, err)
			require.Equal(t, core.AgentStateIdle, rt.State())
			want := int64(1)
			if mode == "failed" {
				want = 2
			}
			require.Equal(t, want, requests.Load(), "explicit resume must execute the previously pending input")
			reply, _ := rt.Snapshot().Self().LastReply()
			require.Equal(t, "resumed", reply)
		})
	}
}

func TestAgentBoundaryEditPrivateSchema(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, srv, agents := agentIdentitySession(t, cache, "boundary-edit")
	query, err := core.CurrentQuery(ctx)
	require.NoError(t, err)
	old := query.Server.(*agentIdentityTestServer)
	env := &llmAuthSecrets{vars: map[string]string{"env://ANTHROPIC_AUTH_TOKEN": "token"}}
	query.Server = &agentEditSchemaServer{agentIdentityTestServer: old, conn: newLLMAuthAttachable(t, env)}

	started, finishFirst := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(finishFirst) }) })
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			close(started)
			select {
			case <-finishFirst:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_%d\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n", n)
		_, _ = fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"response %d\"}}\n\n", n)
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	t.Cleanup(func() { unblock.Do(func() { close(finishFirst) }); provider.Close() })
	old.mainClient.LLMConfig = &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
		"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN", BaseURL: provider.URL},
	}}
	var base dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &base, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("claude-sonnet-4-5"))}}}))
	agent, err := spawnIdentityAgent(ctx, srv, base)
	require.NoError(t, err)
	rt, err := agents.Require(ctx, agent)
	require.NoError(t, err)
	message, err := agents.Send(ctx, agent, "original user request", "")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("step did not start")
	}

	token := []dagql.NamedInput{{Name: "token", Value: dagql.String("edit")}}
	type beginResult struct {
		snapshot dagql.ObjectResult[*core.LLM]
		err      error
	}
	begun := make(chan beginResult, 1)
	go func() {
		var snapshot dagql.ObjectResult[*core.LLM]
		err := srv.Select(ctx, agent, &snapshot, dagql.Selector{Field: "__beginEdit", Args: token})
		begun <- beginResult{snapshot, err}
	}()
	t.Cleanup(func() { rt.AbortConversationEdit("edit") })
	require.Eventually(t, func() bool {
		err := rt.Reseed(ctx, base)
		return err != nil && strings.Contains(err.Error(), "boundary")
	}, time.Second, time.Millisecond)
	select {
	case res := <-begun:
		t.Fatalf("begin did not wait for step: %v", res.err)
	default:
	}
	unblock.Do(func() { close(finishFirst) })
	var result beginResult
	select {
	case result = <-begun:
	case <-time.After(5 * time.Second):
		t.Fatal("edit did not reach boundary")
	}
	require.NoError(t, result.err)
	reply, _ := result.snapshot.Self().LastReply()
	require.Equal(t, "response 1", reply, "the completed step must not be discarded")

	// A second editor and a wrong-token abort cannot disturb the reservation.
	var conflict dagql.ObjectResult[*core.LLM]
	err = srv.Select(ctx, agent, &conflict, dagql.Selector{Field: "__beginEdit", Args: []dagql.NamedInput{{Name: "token", Value: dagql.String("other")}}})
	require.ErrorContains(t, err, "another")
	var id core.AgentID
	require.NoError(t, srv.Select(ctx, agent, &id, dagql.Selector{Field: "__abortEdit", Args: []dagql.NamedInput{{Name: "token", Value: dagql.String("other")}}}))
	response := make(chan string, 1)
	responseErr := make(chan error, 1)
	go func() { reply, err := agents.MessageResponse(ctx, message); response <- reply; responseErr <- err }()
	select {
	case err := <-responseErr:
		t.Fatalf("edit prematurely resolved consumed message: %v", err)
	default:
	}
	queued, err := agents.Send(ctx, agent, "queued during edit", "")
	require.NoError(t, err)

	// Model a compaction result: a replacement conversation ending in a
	// pending summary, with a new effort setting. The original message handle
	// still receives the continuation's reply, not a reseed cancellation.
	var replacement dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, base, &replacement,
		dagql.Selector{Field: "withReasoningEffort", Args: []dagql.NamedInput{{Name: "effort", Value: dagql.String("low")}}},
		dagql.Selector{Field: "withPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("compacted summary")}}}))
	rawID, err := replacement.ID()
	require.NoError(t, err)
	replacementID := dagql.NewID[*core.LLM](rawID)
	require.NoError(t, srv.Select(ctx, agent, &id, dagql.Selector{Field: "__commitEdit", Args: append(token, dagql.NamedInput{Name: "conversation", Value: replacementID})}))
	select {
	case err := <-responseErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("original response handle did not complete")
	}
	require.Equal(t, "response 2", <-response)
	queuedReply, err := agents.MessageResponse(ctx, queued)
	require.NoError(t, err)
	require.Equal(t, "response 2", queuedReply)
	_, err = rt.WaitSettled(ctx)
	require.NoError(t, err)
	require.Equal(t, core.AgentStateIdle, rt.State())
	require.EqualValues(t, 2, requests.Load())
	require.NoError(t, srv.Select(ctx, agent, &id, dagql.Selector{Field: "__abortEdit", Args: token}))

	// Editing idle summary state never starts a fresh turn.
	var idleSnapshot dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, agent, &idleSnapshot, dagql.Selector{Field: "__beginEdit", Args: token}))
	require.NoError(t, srv.Select(ctx, agent, &id, dagql.Selector{Field: "__commitEdit", Args: append(token, dagql.NamedInput{Name: "conversation", Value: replacementID})}))
	require.Equal(t, core.AgentStateIdle, rt.State())
	require.EqualValues(t, 2, requests.Load())
}
