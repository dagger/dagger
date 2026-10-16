package schema

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type agentIdentityTestServer struct {
	*compositionTestServer
	agents *core.AgentRuntimes
}

func (s *agentIdentityTestServer) Agents(context.Context) (*core.AgentRuntimes, error) {
	return s.agents, nil
}

func agentIdentitySession(t *testing.T, cache *dagql.Cache, name string) (context.Context, *dagql.Server, *core.AgentRuntimes) {
	t.Helper()
	md := &engine.ClientMetadata{ClientID: name, SessionID: name}
	var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		return engine.NewClientLifecycleLease(kind, owner, nil, func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			return newLease(kind, owner), nil
		})
	}
	lease := newLease(engine.ClientLeaseRequest, "agent-identity-test")
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(md, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	agents := core.NewAgentRuntimes()
	t.Cleanup(func() { require.NoError(t, agents.KillAll(context.Background(), fmt.Errorf("test complete"))) })
	server := &agentIdentityTestServer{
		compositionTestServer: &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}},
		agents:                agents,
	}
	root := core.NewRoot(server)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	srv, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	server.dag = srv
	server.deps = core.NewSchemaBuilder(root, nil)
	return ctx, srv, agents
}

func agentIdentityLLM(t *testing.T, ctx context.Context, srv *dagql.Server) dagql.ObjectResult[*core.LLM] {
	t.Helper()
	var llm dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &llm, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	return llm
}

func spawnIdentityAgent(ctx context.Context, srv *dagql.Server, llm dagql.ObjectResult[*core.LLM], args ...dagql.NamedInput) (dagql.ObjectResult[*core.Agent], error) {
	var id core.AgentID
	if err := srv.Select(ctx, llm, &id, dagql.Selector{Field: "spawn", Args: args}); err != nil {
		return dagql.ObjectResult[*core.Agent]{}, err
	}
	return id.Load(ctx, srv)
}

func TestAgentHandleReferenceRestore(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, srv, _ := agentIdentitySession(t, cache, "source-agent-session")
	base := agentIdentityLLM(t, ctx, srv)
	require.NoError(t, srv.Select(ctx, base, &base, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String(strings.Repeat("large conversation ", 2000))}}}))
	chief, err := spawnIdentityAgent(ctx, srv, base, dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("chief"))})
	require.NoError(t, err)
	worker, err := spawnIdentityAgent(core.AgentToContext(ctx, chief), srv, base, dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("worker"))})
	require.NoError(t, err)
	publicRecipe, err := worker.RecipeID(ctx)
	require.NoError(t, err)
	require.Equal(t, "agent", publicRecipe.Field(), "spawn keeps its seed-bearing public ID")
	require.NotNil(t, worker.Self().Seed.Self())

	ref, err := core.AgentReference(ctx, worker.Self().Handle)
	require.NoError(t, err)
	require.Nil(t, ref.Self().Seed.Self())
	recipe, err := ref.RecipeID(ctx)
	require.NoError(t, err)
	require.Equal(t, "__agentReference", recipe.Field())
	require.Nil(t, recipe.Receiver())
	require.Empty(t, recipe.ImplicitInputs())
	require.Len(t, recipe.Args(), 1)
	require.Equal(t, worker.Self().Handle, recipe.Arg("handle").Value().ToInput())
	encoded, err := recipe.Encode()
	require.NoError(t, err)
	require.Less(t, len(encoded), 1000, "reference size must not depend on the seed")
	parent, err := (agentSchema{}).parent(ctx, ref, struct{}{})
	require.NoError(t, err)
	require.True(t, parent.Valid)
	require.Equal(t, chief.Self().Handle, parent.Value.Self().Handle)
	require.Nil(t, parent.Value.Self().Seed.Self())
	var rootParent dagql.Nullable[dagql.ObjectResult[*core.Agent]]
	require.NoError(t, srv.Select(ctx, chief, &rootParent, dagql.Selector{Field: "parent"}))
	require.False(t, rootParent.Valid)
	chiefRecipe, err := chief.RecipeID(ctx)
	require.NoError(t, err)
	for _, field := range []string{"seed", "snapshot"} {
		var llm dagql.ObjectResult[*core.LLM]
		require.NoError(t, srv.Select(ctx, ref, &llm, dagql.Selector{Field: field}))
		require.Equal(t, worker.Self().Handle, llm.Self().OwnerAgentHandle)
	}

	// Reuse the cache to prove eligibility cannot leak an old runtime across
	// sessions. The recorded recipe itself has no session key to rewrite.
	dstCtx, dst, _ := agentIdentitySession(t, cache, "restored-agent-session")
	_, err = dst.Load(dstCtx, recipe)
	require.ErrorContains(t, err, "has no runtime entry in this session")
	dstBase := agentIdentityLLM(t, dstCtx, dst)
	ancestor, err := spawnIdentityAgent(dstCtx, dst, dstBase,
		dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("restored-ancestor"))})
	require.NoError(t, err)
	_, err = spawnIdentityAgent(dstCtx, dst, dstBase,
		dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("restored-chief"))},
		dagql.NamedInput{Name: "handle", Value: dagql.Opt(dagql.String(chief.Self().Handle))},
		dagql.NamedInput{Name: "parentHandle", Value: dagql.Opt(dagql.String(ancestor.Self().Handle))})
	require.NoError(t, err)
	// A seed-bearing ID may have cached a null parent in the source session.
	// Replaying it must project the destination runtime's restored lineage.
	restoredChief, err := dst.Load(dstCtx, chiefRecipe)
	require.NoError(t, err)
	require.NoError(t, dst.Select(dstCtx, restoredChief, &rootParent, dagql.Selector{Field: "parent"}))
	require.True(t, rootParent.Valid)
	require.Equal(t, ancestor.Self().Handle, rootParent.Value.Self().Handle)
	_, err = spawnIdentityAgent(dstCtx, dst, dstBase,
		dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("restored-worker"))},
		dagql.NamedInput{Name: "handle", Value: dagql.Opt(dagql.String(worker.Self().Handle))},
		dagql.NamedInput{Name: "parentHandle", Value: dagql.Opt(dagql.String(chief.Self().Handle))})
	require.NoError(t, err)
	restored, err := dst.Load(dstCtx, recipe)
	require.NoError(t, err)
	restoredAgent := restored.(dagql.ObjectResult[*core.Agent])
	require.Equal(t, "restored-worker", restoredAgent.Self().Name)
	parent, err = (agentSchema{}).parent(dstCtx, restoredAgent, struct{}{})
	require.NoError(t, err)
	require.Equal(t, "restored-chief", parent.Value.Self().Name)

	// The owner selector survives independent cold replay of the conversation.
	seedRecipe, err := worker.Self().Seed.RecipeID(ctx)
	require.NoError(t, err)
	coldCache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, coldCache.Close(context.Background())) })
	coldCtx, cold, _ := agentIdentitySession(t, coldCache, "cold-owner-session")
	replayed, err := cold.Load(coldCtx, seedRecipe)
	require.NoError(t, err)
	llm, ok := dagql.UnwrapAs[*core.LLM](replayed)
	require.True(t, ok)
	require.Equal(t, worker.Self().Handle, llm.OwnerAgentHandle)
}

func TestAgentSpawnCompositionFailureRetry(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx, srv, agents := agentIdentitySession(t, cache, "spawn-composition-session")
	base := agentIdentityLLM(t, ctx, srv)
	chief, err := spawnIdentityAgent(ctx, srv, base, dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("chief"))})
	require.NoError(t, err)
	ctx = core.AgentToContext(ctx, chief)

	llmType := objectResult(t, srv, "spawn-llm", objectTypeDef(t, srv, "LLM"))
	agentType := objectResult(t, srv, "spawn-agent", objectTypeDef(t, srv, "Agent"))
	args := dagql.ObjectResultArray[*core.FunctionArg]{
		objectResult(t, srv, "spawn-base", &core.FunctionArg{Name: "base", TypeDef: llmType}),
		objectResult(t, srv, "spawn-me", &core.FunctionArg{Name: "me", TypeDef: agentType}),
	}
	fn := objectResult(t, srv, "spawn-worker", &core.Function{Name: "identityWorker", ReturnType: llmType, Args: args})
	obj := core.NewObjectTypeDef("Query", "", nil)
	obj.Functions = dagql.ObjectResultArray[*core.Function]{fn}
	rootType := objectResult(t, srv, "spawn-root", (&core.TypeDef{}).WithObject(objectResult(t, srv, "spawn-object", obj)))
	mod := objectResult(t, srv, "spawn-module", &core.Module{NameField: "fixture"})
	entry := &core.Expertise{Artifact: &core.Artifact{Path: []string{"fixture", "identity-worker"}, TypeName: "Expertise",
		Node: &core.ModTreeNode{Name: "identityWorker", OriginalModule: mod,
			Parent: &core.ModTreeNode{RootValue: objectResult(t, srv, "spawn-root-value", srv.Root().Unwrap().(*core.Query)), Type: rootType}},
	}}
	fail := true
	expectedParent := chief.Self().Handle
	var owners []dagql.ObjectResult[*core.Agent]
	dagql.Fields[*core.Query]{
		dagql.Func("identityExpertise", func(context.Context, *core.Query, struct{}) (*core.Expertise, error) { return entry.Clone(), nil }),
		dagql.NodeFunc("identityWorker", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct {
			Base core.LLMID
			Me   core.AgentID
		}) (dagql.ObjectResult[*core.LLM], error) {
			llm, err := args.Base.Load(ctx, srv)
			if err != nil {
				return llm, err
			}
			me, err := args.Me.Load(ctx, srv)
			if err != nil {
				return llm, err
			}
			owners = append(owners, me)
			require.Equal(t, llm.Self().OwnerAgentHandle, me.Self().Handle)
			require.NotEqual(t, chief.Self().Handle, me.Self().Handle)
			require.Nil(t, me.Self().Seed.Self())
			parent, err := (agentSchema{}).parent(ctx, me, struct{}{})
			require.NoError(t, err)
			require.Equal(t, expectedParent, parent.Value.Self().Handle)
			caller, ok := core.CallerAgent(ctx)
			require.True(t, ok)
			require.Equal(t, chief.Self().Handle, caller.Self().Handle, "composition lineage must not replace message provenance")
			_, err = agents.Send(ctx, me, "too soon", "")
			require.ErrorContains(t, err, "still composing")
			if fail {
				return llm, fmt.Errorf("fixture composition failure")
			}
			err = srv.Select(ctx, llm, &llm, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("worker ready")}}})
			return llm, err
		}),
	}.Install(srv)
	var expertise dagql.ObjectResult[*core.Expertise]
	require.NoError(t, srv.Select(ctx, srv.Root(), &expertise, dagql.Selector{Field: "identityExpertise"}))
	id, err := expertise.ID()
	require.NoError(t, err)
	spawnArgs := []dagql.NamedInput{
		{Name: "name", Value: dagql.Opt(dagql.String("worker"))},
		{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]]{dagql.NewID[*core.Expertise](id)}},
	}
	// Run the spawn from a real parent expertise entry. The child's compose
	// must start its own record without losing CallerAgent.
	parentFn := objectResult(t, srv, "spawn-parent", &core.Function{Name: "spawnWorker", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{args[0]}})
	parentObj := core.NewObjectTypeDef("Query", "", nil)
	parentObj.Functions = dagql.ObjectResultArray[*core.Function]{parentFn}
	parentType := objectResult(t, srv, "spawn-parent-type", (&core.TypeDef{}).WithObject(objectResult(t, srv, "spawn-parent-object", parentObj)))
	parentEntry := entry.Clone()
	parentEntry.Artifact.Path = []string{"fixture", "spawn-worker"}
	parentEntry.Artifact.Node.Name = "spawnWorker"
	parentEntry.Artifact.Node.Parent.Type = parentType
	parentKey, err := parentEntry.Identity()
	require.NoError(t, err)
	var worker dagql.ObjectResult[*core.Agent]
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("spawnWorker", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct{ Base core.LLMID }) (dagql.ObjectResult[*core.LLM], error) {
			llm, err := args.Base.Load(ctx, srv)
			if err != nil {
				return llm, err
			}
			if llm.Self().OwnerAgentHandle != "" {
				expectedParent = llm.Self().OwnerAgentHandle
			}
			worker, err = spawnIdentityAgent(ctx, srv, llm, spawnArgs...)
			return llm, err
		}).DoNotCache("The fixture spawns a fresh worker on each attempt."),
	}.Install(srv)
	_, err = parentEntry.Run(ctx, base)
	require.ErrorContains(t, err, "left STOPPED")
	require.ErrorContains(t, err, "fixture composition failure")
	require.Len(t, owners, 1)
	rt, found, err := agents.Get(ctx, owners[0])
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, core.AgentStateStopped, rt.State())
	fail = false
	_, err = parentEntry.Run(ctx, base)
	require.NoError(t, err)
	require.Len(t, owners, 2)
	require.NotEqual(t, owners[0].Self().Handle, worker.Self().Handle)
	require.Equal(t, "worker", worker.Self().Name)
	require.Equal(t, worker.Self().Handle, worker.Self().Seed.Self().OwnerAgentHandle)
	require.Equal(t, "worker ready", worker.Self().Seed.Self().Messages[0].TextContent())
	childSeed := worker.Self().Seed.Self()
	require.Len(t, childSeed.Expertise, 1, "a spawned conversation records its own entries")
	childKey, err := childSeed.Expertise[0].Self().Identity()
	require.NoError(t, err)
	wantKey, err := entry.Identity()
	require.NoError(t, err)
	require.Equal(t, wantKey, childKey)
	require.NotEqual(t, parentKey, childKey)
	require.Equal(t, "fixture", childSeed.Messages[0].CompositionOwner,
		"the fixture's prompt is made outside a module, but while its entry ran: the entry's module claims it")
	rt, err = agents.Require(ctx, worker)
	require.NoError(t, err)
	require.Equal(t, core.AgentStateIdle, rt.State())

	// Initial composition runs on the new family's behalf for lineage only:
	// CallerAgent is still chief, but the nested worker's parent is family.
	dagql.Fields[*core.Query]{
		dagql.Func("parentExpertise", func(context.Context, *core.Query, struct{}) (*core.Expertise, error) { return parentEntry.Clone(), nil }),
	}.Install(srv)
	var parentExpertise dagql.ObjectResult[*core.Expertise]
	require.NoError(t, srv.Select(ctx, srv.Root(), &parentExpertise, dagql.Selector{Field: "parentExpertise"}))
	parentID, err := parentExpertise.ID()
	require.NoError(t, err)
	family, err := spawnIdentityAgent(ctx, srv, base,
		dagql.NamedInput{Name: "name", Value: dagql.Opt(dagql.String("family"))},
		dagql.NamedInput{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]]{dagql.NewID[*core.Expertise](parentID)}})
	require.NoError(t, err)
	familyParent, err := (agentSchema{}).parent(ctx, family, struct{}{})
	require.NoError(t, err)
	require.Equal(t, chief.Self().Handle, familyParent.Value.Self().Handle)
	workerParent, err := (agentSchema{}).parent(ctx, worker, struct{}{})
	require.NoError(t, err)
	require.Equal(t, family.Self().Handle, workerParent.Value.Self().Handle)
	require.Equal(t, "family", workerParent.Value.Self().Name)
	// The parent entry spawned from the conversation it was composing: the
	// child's composition is not nested in it, and records only its own entry.
	childSeed = worker.Self().Seed.Self()
	require.Len(t, childSeed.Expertise, 1)
	childKey, err = childSeed.Expertise[0].Self().Identity()
	require.NoError(t, err)
	require.Equal(t, wantKey, childKey)
	require.Empty(t, childSeed.ComposingExpertise)
	require.Equal(t, "worker ready", childSeed.Messages[0].TextContent())
}
