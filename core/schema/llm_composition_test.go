package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type compositionTestServer struct {
	*currentTypeDefsTestServer
	module    dagql.ObjectResult[*core.Module]
	moduleErr error
}

func (s *compositionTestServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return s.module, s.moduleErr
}

func TestLLMCompositionOwnerSelectorsRoundTrip(t *testing.T) {
	md := &engine.ClientMetadata{ClientID: "composition-client", SessionID: "composition-session"}
	ctx := engine.ContextWithClientMetadata(t.Context(), md)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	server := &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}}
	root := core.NewRoot(server)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	srv, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	server.dag = srv

	var tool dagql.ObjectResult[*core.TypeDef]
	require.NoError(t, srv.Select(ctx, srv.Root(), &tool, dagql.Selector{Field: "typeDef"}))
	toolID, err := tool.ID()
	require.NoError(t, err)
	var dir dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &dir, dagql.Selector{Field: "directory"}))
	dirID, err := dir.ID()
	require.NoError(t, err)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed,
		dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
		dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}},
	))
	setCaller := func(name string) {
		t.Helper()
		server.module = dagql.ObjectResult[*core.Module]{}
		if name != "" {
			mod := &core.Module{NameField: name, OriginalName: "intrinsic-name"}
			server.module, err = dagql.NewObjectResultForCall(mod, srv, implementationScopedTestSyntheticCall("caller-"+name, mod))
			require.NoError(t, err)
		}
	}
	for _, test := range []struct {
		name   string
		caller string
		entry  string
		owner  dagql.Optional[dagql.String]
		want   string
	}{
		{"main client is unowned", "", "", dagql.Optional[dagql.String]{}, ""},
		{"ordinary module is unowned", "group-A", "", dagql.Optional[dagql.String]{}, ""},
		{"omitted uses entry A", "helper", "group-A", dagql.Optional[dagql.String]{}, "group-A"},
		{"omitted uses entry B", "helper", "group-B", dagql.Optional[dagql.String]{}, "group-B"},
		{"explicit empty is unowned", "helper", "group-A", dagql.Opt(dagql.String("")), ""},
		{"explicit other owner", "helper", "group-A", dagql.Opt(dagql.String("group-B")), "group-B"},
		{"owner prefix collision", "helper", "group-A", dagql.Opt(dagql.String("group-AB")), "group-AB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			replayScope := core.WithExpertiseOwner(ctx, "replay-entry")
			ctx := ctx
			if test.entry != "" {
				ctx = core.WithExpertiseOwner(ctx, test.entry)
				ctx = core.WithExpertiseOwner(ctx, "nested-entry")
			}
			setCaller(test.caller)
			promptArgs := []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}
			toolArgs := []dagql.NamedInput{{Name: "object", Value: dagql.NewAnyID(toolID)}}
			skillArgs := []dagql.NamedInput{{Name: "directory", Value: dagql.NewID[*core.Directory](dirID)}}
			if test.owner.Valid {
				promptArgs = append(promptArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
				toolArgs = append(toolArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
				skillArgs = append(skillArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
			}
			// Each call uses the same receiver and inputs across callers, so a
			// cache key missing the owner would return another caller's stamp.
			for _, sel := range []dagql.Selector{
				{Field: "withSystemPrompt", Args: promptArgs},
				{Field: "withTools", Args: toolArgs},
				{Field: "withSkills", Args: skillArgs},
			} {
				var result dagql.ObjectResult[*core.LLM]
				require.NoError(t, srv.Select(ctx, seed, &result, sel))
				id, err := result.RecipeID(ctx)
				require.NoError(t, err)
				require.NotNil(t, id.Arg("owner"))
				require.Equal(t, test.want, id.Arg("owner").Value().ToInput())
			}
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, seed, &llm,
				dagql.Selector{Field: "withSystemPrompt", Args: promptArgs},
				dagql.Selector{Field: "withTools", Args: toolArgs},
				dagql.Selector{Field: "withSkills", Args: skillArgs},
			))
			require.Empty(t, llm.Self().Messages[0].CompositionOwner)
			require.Equal(t, test.want, llm.Self().Messages[1].CompositionOwner)

			setCaller("replay-caller")
			recipe, err := llm.RecipeID(ctx)
			require.NoError(t, err)
			encoded, err := recipe.Encode()
			require.NoError(t, err)
			var decoded call.ID
			require.NoError(t, decoded.Decode(encoded))
			// Replay on a cold cache, with no expertise scope inherited from the
			// original call. The explicit stamps must restore all contributions.
			replayCache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			replayCtx := dagql.ContextWithCache(replayScope, replayCache)
			replayed, err := srv.Load(replayCtx, &decoded)
			require.NoError(t, err)
			restored, ok := dagql.UnwrapAs[*core.LLM](replayed)
			require.True(t, ok)
			require.Equal(t, test.want, restored.Messages[1].CompositionOwner)
			require.Equal(t, []string{test.want}, compositionRecipeOwners(t, &decoded, "withTools"))
			require.Equal(t, []string{test.want}, compositionRecipeOwners(t, &decoded, "withSkills"))

			var removed dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, llm, &removed, dagql.Selector{
				Field: "__withoutComposition", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String("group-A")}},
			}))
			if test.want == "group-A" {
				require.Len(t, removed.Self().Messages, 1)
			} else {
				require.Len(t, removed.Self().Messages, 2)
			}
		})
	}

	// The explicit owner override is marked as engine-internal replay plumbing
	// so author SDKs and tool schemas do not offer it as a normal argument.
	llmType, ok := srv.ObjectType("LLM")
	require.True(t, ok)
	for _, view := range []call.View{"v0.19.0", "v1.0.0"} {
		_, ok := llmType.FieldSpec("__withCompositionOwner", view)
		require.False(t, ok)
	}
	_, ok = llmType.FieldSpec("__withoutComposition", "v0.19.0")
	require.False(t, ok)
	_, ok = llmType.FieldSpec("__withoutComposition", "v1.0.0")
	require.True(t, ok)
	setCaller("")
	// Module identity is no longer consulted for contribution ownership.
	for _, moduleErr := range []error{core.ErrNoCurrentModule, fmt.Errorf("caller lookup failed")} {
		server.moduleErr = moduleErr
		var result dagql.ObjectResult[*core.LLM]
		err := srv.Select(ctx, seed, &result, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("lookup")}}})
		require.NoError(t, err)
		require.Empty(t, result.Self().Messages[1].CompositionOwner)
	}
	server.moduleErr = nil
	// Default and explicit versions are recorded on the original call frame.
	for _, version := range []int{1, 0, 7} {
		t.Run(fmt.Sprintf("binding version %d", version), func(t *testing.T) {
			toolArgs := []dagql.NamedInput{{Name: "object", Value: dagql.NewAnyID(toolID)}}
			if version != 0 {
				toolArgs = append(toolArgs, dagql.NamedInput{Name: "version", Value: dagql.Int(version)})
			}
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, srv.Root(), &llm,
				dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
				dagql.Selector{Field: "withTools", Args: toolArgs},
			))
			recipe, err := llm.RecipeID(ctx)
			require.NoError(t, err)
			found := false
			for id := recipe; id != nil; id = id.Receiver() {
				if id.Field() != "withTools" {
					continue
				}
				found = true
				arg := id.Arg("version")
				if version == 0 {
					require.Nil(t, arg, "the default need not be materialized")
				} else {
					require.NotNil(t, arg)
					require.EqualValues(t, version, arg.Value().ToInput())
				}
			}
			require.True(t, found)
		})
	}

	for _, name := range []string{"withTools", "withSystemPrompt", "withSkills"} {
		field, ok := llmType.FieldSpec(name, srv.View)
		require.True(t, ok)
		owner, ok := field.Args.Input("owner", srv.View)
		require.True(t, ok)
		require.True(t, owner.Internal)
		require.NotNil(t, field.FieldDefinition(srv.View).Arguments.ForName("owner").Directives.ForName("internal"))
	}
}

func TestLLMComposeScopeCacheAndReplay(t *testing.T) {
	for _, field := range []string{"compose", "recompose"} {
		for _, nestedFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nestedFirst=%t", field, nestedFirst), func(t *testing.T) {
				runs := 0
				newSession := func() (context.Context, *dagql.Server) {
					md := &engine.ClientMetadata{ClientID: "compose-scope", SessionID: "compose-scope"}
					ctx := engine.ContextWithClientMetadata(t.Context(), md)
					cache, err := dagql.NewCache(ctx, "", nil, nil)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
					ctx = dagql.ContextWithCache(ctx, cache)
					server := &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}}
					root := core.NewRoot(server)
					ctx = core.ContextWithQuery(ctx, root)
					base, err := NewCoreSchemaBase(ctx, server)
					require.NoError(t, err)
					srv, err := base.Fork(ctx, root, "")
					require.NoError(t, err)
					server.dag = srv
					server.deps = core.NewSchemaBuilder(root, nil)

					// A native fixture entry exercises the real Expertise.Run path
					// without a container runtime. Its schema matches a module call.
					llmType := objectResult(t, srv, "scope-llm", objectTypeDef(t, srv, "LLM"))
					arg := objectResult(t, srv, "scope-base", &core.FunctionArg{Name: "base", TypeDef: llmType})
					fn := objectResult(t, srv, "scope-function", &core.Function{Name: "contribute", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg}})
					obj := core.NewObjectTypeDef("Query", "", nil)
					obj.Functions = dagql.ObjectResultArray[*core.Function]{fn}
					objRes := objectResult(t, srv, "scope-object", obj)
					rootType := objectResult(t, srv, "scope-root", (&core.TypeDef{}).WithObject(objRes))
					mod := objectResult(t, srv, "scope-module", &core.Module{NameField: "fixture"})
					entry := &core.Expertise{Arguments: core.JSON("{}"), Artifact: &core.Artifact{
						Path: []string{"fixture", "contribute"}, TypeName: "Expertise", Directives: []string{"agent"},
						Node: &core.ModTreeNode{Name: "contribute", OriginalModule: mod,
							Parent: &core.ModTreeNode{RootValue: objectResult(t, srv, "scope-root-value", root), Type: rootType}},
					}}
					contribute := dagql.NodeFunc("contribute", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct {
						Base  dagql.ID[*core.LLM]
						Owner dagql.Optional[dagql.String] `name:"_expertiseOwner" internal:"true"`
					}) (dagql.ObjectResult[*core.LLM], error) {
						runs++
						ctx = core.RestoreExpertiseOwner(ctx, string(args.Owner.Value))
						llm, err := args.Base.Load(ctx, srv)
						if err != nil {
							return llm, err
						}
						err = srv.Select(ctx, llm, &llm, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("fixture prompt")}}})
						return llm, err
					})
					contribute.Spec.GetDynamicInput = func(ctx context.Context, _ dagql.AnyResult, _ map[string]dagql.Input, _ call.View, req *dagql.CallRequest) error {
						return core.StampExpertiseOwner(ctx, req)
					}
					dagql.Fields[*core.Query]{
						contribute,
						dagql.Func("fixtureExpertise", func(context.Context, *core.Query, struct{}) (*core.Expertise, error) { return entry.Clone(), nil }),
					}.Install(srv)
					return ctx, srv
				}

				ctx, srv := newSession()
				var seed dagql.ObjectResult[*core.LLM]
				require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
				var entry dagql.ObjectResult[*core.Expertise]
				require.NoError(t, srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: "fixtureExpertise"}))
				entryID, err := entry.ID()
				require.NoError(t, err)
				key, err := entry.Self().Identity()
				require.NoError(t, err)
				selection := dagql.Selector{Field: field, Args: []dagql.NamedInput{{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]]{dagql.NewID[*core.Expertise](entryID)}}}}
				assertResult := func(llm *core.LLM, nested bool) {
					t.Helper()
					require.Len(t, llm.Messages, 1)
					if nested {
						require.Equal(t, "outer-entry", llm.Messages[0].CompositionOwner)
						require.Empty(t, llm.Expertise)
					} else {
						require.Equal(t, key, llm.Messages[0].CompositionOwner)
						require.Len(t, llm.Expertise, 1)
					}
				}
				for _, nested := range []bool{nestedFirst, !nestedFirst} {
					callCtx := ctx
					if nested {
						callCtx = core.WithExpertiseOwner(ctx, "outer-entry")
					}
					for range 2 {
						var result dagql.ObjectResult[*core.LLM]
						require.NoError(t, srv.Select(callCtx, seed, &result, selection))
						assertResult(result.Self(), nested)
					}
				}
				require.Equal(t, 2, runs, "top-level and nested composition must not share cached results")

				// Replay the composition call itself, not its returned-result alias,
				// so cold loading must restore the scope before running the entry.
				seedRecipe, err := seed.RecipeID(ctx)
				require.NoError(t, err)
				entryRecipe, err := entry.RecipeID(ctx)
				require.NoError(t, err)
				for _, nested := range []bool{false, true} {
					args := []*call.Argument{call.NewArgument("expertise", call.NewLiteralList(call.NewLiteralID(entryRecipe)), false)}
					if nested {
						args = append(args, call.NewArgument("_expertiseOwner", call.NewLiteralString("outer-entry"), false))
					}
					recipe := seedRecipe.Append((&core.LLM{}).Type(), field, call.WithArgs(args...))
					replayCtx, replaySrv := newSession()
					replayCtx = core.WithExpertiseOwner(replayCtx, "different-replay-entry")
					replayed, err := replaySrv.Load(replayCtx, recipe)
					require.NoError(t, err)
					llm, ok := dagql.UnwrapAs[*core.LLM](replayed)
					require.True(t, ok)
					assertResult(llm, nested)
				}
				require.Equal(t, 4, runs, "cold replay must execute each entry in its recorded scope")
			})
		}
	}
}

func TestLLMRestoreSkipsSupersededToolConstruction(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, sameSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("warm=%t/sameSession=%t", warm, sameSession), func(t *testing.T) {
				md := &engine.ClientMetadata{ClientID: "source", SessionID: "source"}
				ctx := engine.ContextWithClientMetadata(t.Context(), md)
				cache, err := dagql.NewCache(ctx, "", nil, nil)
				require.NoError(t, err)
				ctx = dagql.ContextWithCache(ctx, cache)
				server := &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}}
				root := core.NewRoot(server)
				ctx = core.ContextWithQuery(ctx, root)
				base, err := NewCoreSchemaBase(ctx, server)
				require.NoError(t, err)
				srv, err := base.Fork(ctx, root, "")
				require.NoError(t, err)
				server.dag = srv
				const resource = dagql.SessionResourceHandle("superseded-tool-resource")
				require.NoError(t, cache.BindSessionResource(ctx, md.SessionID, md.ClientID, resource, "source-only"))
				calls := 0
				fail := false
				dagql.Fields[*core.Query]{
					dagql.NodeFunc("replacementTool", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], _ struct{ Name string }) (dagql.ObjectResult[*core.TypeDef], error) {
						calls++
						if fail {
							return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("tool construction must remain lazy")
						}
						obj, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &core.TypeDef{})
						if err != nil {
							return obj, err
						}
						return obj.WithSessionResourceHandle(ctx, resource)
					}),
				}.Install(srv)
				var seed dagql.ObjectResult[*core.LLM]
				require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{
					Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}},
				}))
				recipe, err := seed.RecipeID(ctx)
				require.NoError(t, err)
				for _, name := range []string{"A", "B"} {
					objectRecipe := call.New().Append((&core.TypeDef{}).Type(), "replacementTool",
						call.WithArgs(call.NewArgument("name", call.NewLiteralString(name), false)))
					if warm {
						_, err := srv.Load(ctx, objectRecipe)
						require.NoError(t, err)
					}
					recipe = recipe.Append((&core.LLM{}).Type(), "withTools", call.WithArgs(
						call.NewArgument("object", call.NewLiteralID(objectRecipe), false),
						call.NewArgument("owner", call.NewLiteralString("owner-"+name), false),
					))
				}
				calls, fail = 0, true
				if !sameSession {
					ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "destination", SessionID: "destination"})
				}
				if !warm {
					cache, err = dagql.NewCache(ctx, "", nil, nil)
					require.NoError(t, err)
					ctx = dagql.ContextWithCache(ctx, cache)
				}
				_, err = srv.Load(ctx, recipe)
				require.NoError(t, err)
				require.Zero(t, calls, "loading the final LLM must not construct either tool object")
			})
		}
	}
}

func TestLLMCommittedLeafRestore(t *testing.T) {
	newSession := func(name string) (context.Context, *dagql.Server) {
		md := &engine.ClientMetadata{ClientID: name, SessionID: name}
		ctx := engine.ContextWithClientMetadata(t.Context(), md)
		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		ctx = dagql.ContextWithCache(ctx, cache)
		server := &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}}
		root := core.NewRoot(server)
		ctx = core.ContextWithQuery(ctx, root)
		base, err := NewCoreSchemaBase(ctx, server)
		require.NoError(t, err)
		srv, err := base.Fork(ctx, root, "")
		require.NoError(t, err)
		server.dag = srv
		return ctx, srv
	}
	ctx, srv := newSession("source")
	frames := map[string]*callpbv1.Call{}
	content, err := (dagql.ArrayInput[dagql.InputObject[core.LLMContentBlockInput]]{}).Decoder().DecodeInput([]any{
		map[string]any{"kind": "TEXT", "text": "recorded answer", "arguments": ""},
		map[string]any{"kind": "TOOL_CALL", "callId": "call-1", "toolName": "tool", "arguments": "{}"},
	})
	require.NoError(t, err)
	var committed dagql.ObjectResult[*core.LLM]
	receiver := srv.Root()
	for _, sel := range []dagql.Selector{
		{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
		{Field: "withPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("hello")}}},
		{Field: "withResponse", Args: []dagql.NamedInput{{Name: "content", Value: content}}},
		{Field: "withToolResult", Args: []dagql.NamedInput{
			{Name: "callId", Value: dagql.String("call-1")},
			{Name: "content", Value: dagql.String("recorded result")},
			{Name: "errored", Value: dagql.Boolean(false)},
		}},
		{Field: "withReasoningEffort", Args: []dagql.NamedInput{{Name: "effort", Value: dagql.String("low")}}},
		{Field: "withReasoningEffort", Args: []dagql.NamedInput{{Name: "effort", Value: dagql.String("high")}}},
	} {
		require.NoError(t, srv.Select(ctx, receiver, &committed, sel))
		frame, err := committed.ResultCall()
		require.NoError(t, err)
		payload, err := frame.CallPB(ctx)
		require.NoError(t, err)
		frames[payload.Digest] = payload
		receiver = committed
	}
	leaf, err := committed.RecipeDigest(ctx)
	require.NoError(t, err)
	require.Len(t, frames, 6, "keep original calls, including superseded state setters")

	// The consumer, not the producer, assembles the delivered individual frames
	// around the committed leaf. A new cache has no source-session handles.
	var recipe call.ID
	require.NoError(t, recipe.FromProto(&callpbv1.DAG{Value: &callpbv1.DAG_Recipe{Recipe: &callpbv1.RecipeDAG{
		RootDigest: leaf.String(), CallsByDigest: frames,
	}}}))
	require.Equal(t, leaf, recipe.Digest())
	dstCtx, dst := newSession("destination")
	loaded, err := dst.Load(dstCtx, &recipe)
	require.NoError(t, err)
	restored := loaded.(dagql.ObjectResult[*core.LLM])
	require.Equal(t, committed.Self().Messages, restored.Self().Messages)
	require.Equal(t, "recorded answer", restored.Self().Messages[1].TextContent())
	require.Equal(t, "recorded result", restored.Self().Messages[2].ToolResultContent())
}

func compositionRecipeOwners(t *testing.T, recipe *call.ID, field string) []string {
	t.Helper()
	var owners []string
	for id := recipe; id != nil; id = id.Receiver() {
		if id.Field() == field {
			arg := id.Arg("owner")
			require.NotNil(t, arg)
			owner, ok := arg.Value().ToInput().(string)
			require.True(t, ok)
			owners = append(owners, owner)
		}
	}
	return owners
}
