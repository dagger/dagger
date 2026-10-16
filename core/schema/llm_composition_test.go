package schema

import (
	"context"
	"fmt"
	"strings"
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
		owner  dagql.Optional[dagql.String]
		want   string
	}{
		{"main client is unowned", "", dagql.Optional[dagql.String]{}, ""},
		{"omitted uses caller A", "group-A", dagql.Optional[dagql.String]{}, "group-A"},
		{"omitted uses caller B", "group-B", dagql.Optional[dagql.String]{}, "group-B"},
		{"explicit empty is unowned", "group-A", dagql.Opt(dagql.String("")), ""},
		{"explicit other owner", "group-A", dagql.Opt(dagql.String("group-B")), "group-B"},
		{"owner prefix collision", "group-A", dagql.Opt(dagql.String("group-AB")), "group-AB"},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			// Replay on a cold cache, called by another module. The explicit
			// stamps must restore every contribution's original owner.
			replayCache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			replayCtx := dagql.ContextWithCache(ctx, replayCache)
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
	// No-current-module is the normal main-client case; other errors must not
	// silently turn a module's contributions into unowned state.
	for _, moduleErr := range []error{core.ErrNoCurrentModule, fmt.Errorf("caller lookup failed")} {
		server.moduleErr = moduleErr
		var result dagql.ObjectResult[*core.LLM]
		err := srv.Select(ctx, seed, &result, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("lookup")}}})
		if moduleErr == core.ErrNoCurrentModule {
			require.NoError(t, err)
			require.Empty(t, result.Self().Messages[1].CompositionOwner)
		} else {
			require.ErrorContains(t, err, moduleErr.Error())
		}
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

// newCompositionScopeSession serves a native fixture entry, fixtureExpertise,
// that exercises the real Expertise.Run path without a container runtime. Its
// schema matches a module call; each execution increments runs.
func newCompositionScopeSession(t *testing.T, runs *int) (context.Context, *dagql.Server) {
	t.Helper()
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

	llmType := objectResult(t, srv, "scope-llm", objectTypeDef(t, srv, "LLM"))
	arg := objectResult(t, srv, "scope-base", &core.FunctionArg{Name: "base", TypeDef: llmType})
	fn := objectResult(t, srv, "scope-function", &core.Function{Name: "contribute", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg}})
	// Entries taking the conversation's owning agent: required, then optional.
	agentType := objectTypeDef(t, srv, "Agent")
	requiredMe := objectResult(t, srv, "scope-required-me", &core.FunctionArg{Name: "me", TypeDef: objectResult(t, srv, "scope-agent", agentType)})
	optionalMe := objectResult(t, srv, "scope-optional-me", &core.FunctionArg{Name: "me", TypeDef: objectResult(t, srv, "scope-optional-agent", agentType.WithOptional(true))})
	requiredFn := objectResult(t, srv, "scope-required-function", &core.Function{Name: "contributeAsAgent", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg, requiredMe}})
	optionalFn := objectResult(t, srv, "scope-optional-function", &core.Function{Name: "contributeMaybeAgent", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg, optionalMe}})
	// An entry reporting how many messages its base already holds, to observe
	// which contributions were cleared before it ran.
	observingFn := objectResult(t, srv, "scope-observing-function", &core.Function{Name: "contributeObserving", ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg}})
	plainFn := func(name string) dagql.ObjectResult[*core.Function] {
		return objectResult(t, srv, "scope-"+name, &core.Function{Name: name, ReturnType: llmType, Args: dagql.ObjectResultArray[*core.FunctionArg]{arg}})
	}
	obj := core.NewObjectTypeDef("Query", "", nil)
	obj.Functions = dagql.ObjectResultArray[*core.Function]{fn, requiredFn, optionalFn, observingFn,
		plainFn("otherContribute"), plainFn("composeSibling"), plainFn("composeSelf"),
		plainFn("contributeViaDependency"), plainFn("composeOther")}
	objRes := objectResult(t, srv, "scope-object", obj)
	rootType := objectResult(t, srv, "scope-root", (&core.TypeDef{}).WithObject(objRes))
	// Every entry belongs to the fixture module, except otherContribute. The
	// donor module has no entries: it stands in for a dependency whose
	// function an entry calls.
	fixtureMod := objectResult(t, srv, "scope-module", &core.Module{NameField: "fixture"})
	otherMod := objectResult(t, srv, "scope-other-module", &core.Module{NameField: "other"})
	donorMod := objectResult(t, srv, "scope-donor-module", &core.Module{NameField: "donor"})
	moduleOf := func(name string) dagql.ObjectResult[*core.Module] {
		switch name {
		case "otherContribute":
			return otherMod
		case "donorHelper":
			return donorMod
		}
		return fixtureMod
	}
	rootValue := objectResult(t, srv, "scope-root-value", root)
	newEntry := func(name string) *core.Expertise {
		return &core.Expertise{Artifact: &core.Artifact{
			Path: []string{moduleOf(name).Self().Name(), name}, TypeName: "Expertise", Directives: []string{"agent"},
			Node: &core.ModTreeNode{Name: name, OriginalModule: moduleOf(name),
				Parent: &core.ModTreeNode{RootValue: rootValue, Type: rootType}},
		}}
	}
	entry := newEntry("contribute")
	// The fixture functions stand in for module functions: like a module's
	// code, they own the contributions they make under their module's name.
	prompt := func(ctx context.Context, base dagql.ID[*core.LLM], fn, text string) (dagql.ObjectResult[*core.LLM], error) {
		*runs++
		llm, err := base.Load(ctx, srv)
		if err != nil {
			return llm, err
		}
		err = srv.Select(ctx, llm, &llm, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{
			{Name: "prompt", Value: dagql.String(text)},
			{Name: "owner", Value: dagql.Opt(dagql.String(core.CompositionOwner(moduleOf(fn).Self())))},
		}})
		return llm, err
	}
	// composeNested composes the named entry into the base its caller is
	// running with, then contributes text.
	composeNested := func(ctx context.Context, base dagql.ID[*core.LLM], fn, nested, text string) (dagql.ObjectResult[*core.LLM], error) {
		llm, err := base.Load(ctx, srv)
		if err != nil {
			return llm, err
		}
		var entry dagql.ObjectResult[*core.Expertise]
		if err := srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: "fixtureEntry", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(nested)}}}); err != nil {
			return llm, err
		}
		entryID, err := entry.ID()
		if err != nil {
			return llm, err
		}
		if err := srv.Select(ctx, llm, &llm, dagql.Selector{Field: "compose", Args: []dagql.NamedInput{
			{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]]{dagql.NewID[*core.Expertise](entryID)}},
		}}); err != nil {
			return llm, err
		}
		id, err := llm.ID()
		if err != nil {
			return llm, err
		}
		return prompt(ctx, dagql.NewID[*core.LLM](id), fn, text)
	}
	type baseArgs struct{ Base dagql.ID[*core.LLM] }
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("contribute", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			return prompt(ctx, args.Base, "contribute", "fixture prompt")
		}),
		dagql.NodeFunc("otherContribute", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			return prompt(ctx, args.Base, "otherContribute", "other prompt")
		}),
		dagql.NodeFunc("contributeAsAgent", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct {
			Base dagql.ID[*core.LLM]
			Me   dagql.ID[*core.Agent]
		}) (dagql.ObjectResult[*core.LLM], error) {
			return prompt(ctx, args.Base, "contributeAsAgent", "agent prompt")
		}),
		dagql.NodeFunc("contributeMaybeAgent", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct {
			Base dagql.ID[*core.LLM]
			Me   dagql.Optional[dagql.ID[*core.Agent]]
		}) (dagql.ObjectResult[*core.LLM], error) {
			text := "maybe agent: null"
			if args.Me.Valid {
				text = "maybe agent: set"
			}
			return prompt(ctx, args.Base, "contributeMaybeAgent", text)
		}),
		dagql.NodeFunc("contributeObserving", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			base, err := args.Base.Load(ctx, srv)
			if err != nil {
				return base, err
			}
			return prompt(ctx, args.Base, "contributeObserving", fmt.Sprintf("observed %d messages", len(base.Self().Messages)))
		}),
		// An entry composing another entry of its own module.
		dagql.NodeFunc("composeSibling", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			return composeNested(ctx, args.Base, "composeSibling", "contribute", "sibling composer")
		}),
		// An entry composing itself.
		dagql.NodeFunc("composeSelf", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			return composeNested(ctx, args.Base, "composeSelf", "composeSelf", "self composer")
		}),
		// An entry composing another module's entry.
		dagql.NodeFunc("composeOther", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			return composeNested(ctx, args.Base, "composeOther", "otherContribute", "other composer")
		}),
		// An entry calling a dependency's function that contributes under the
		// dependency's name, then contributing its own prompt.
		dagql.NodeFunc("contributeViaDependency", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args baseArgs) (dagql.ObjectResult[*core.LLM], error) {
			llm, err := prompt(ctx, args.Base, "donorHelper", "donor prompt")
			if err != nil {
				return llm, err
			}
			id, err := llm.ID()
			if err != nil {
				return llm, err
			}
			return prompt(ctx, dagql.NewID[*core.LLM](id), "contributeViaDependency", "via dependency")
		}),
		dagql.Func("fixtureEntry", func(_ context.Context, _ *core.Query, args struct{ Name string }) (*core.Expertise, error) {
			return newEntry(args.Name), nil
		}),
		dagql.Func("fixtureExpertise", func(context.Context, *core.Query, struct{}) (*core.Expertise, error) { return entry.Clone(), nil }),
		dagql.Func("fixtureObservingExpertise", func(context.Context, *core.Query, struct{}) (*core.Expertise, error) {
			return newEntry("contributeObserving"), nil
		}),
		dagql.Func("fixtureAgentExpertise", func(_ context.Context, _ *core.Query, args struct{ Optional bool }) (*core.Expertise, error) {
			if args.Optional {
				return newEntry("contributeMaybeAgent"), nil
			}
			return newEntry("contributeAsAgent"), nil
		}),
	}.Install(srv)
	return ctx, srv
}

// fixtureEntryID selects the fixture entry running the named function.
func fixtureEntryID(t *testing.T, ctx context.Context, srv *dagql.Server, name string) dagql.ID[*core.Expertise] {
	t.Helper()
	var entry dagql.ObjectResult[*core.Expertise]
	require.NoError(t, srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: "fixtureEntry", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}}}))
	id, err := entry.ID()
	require.NoError(t, err)
	return dagql.NewID[*core.Expertise](id)
}

// composeExpertise selects LLM.compose with the given entries.
func composeExpertise(t *testing.T, ctx context.Context, srv *dagql.Server, llm dagql.ObjectResult[*core.LLM], ids ...dagql.ID[*core.Expertise]) dagql.ObjectResult[*core.LLM] {
	t.Helper()
	var out dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, llm, &out, dagql.Selector{Field: "compose", Args: []dagql.NamedInput{{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]](ids)}}}))
	return out
}

// recordIDs returns the conversation's recorded entries, to compose the whole
// record again: the reload pattern without a workspace to look them up in.
func recordIDs(t *testing.T, llm dagql.ObjectResult[*core.LLM]) []dagql.ID[*core.Expertise] {
	t.Helper()
	ids := make([]dagql.ID[*core.Expertise], len(llm.Self().Expertise))
	for i, entry := range llm.Self().Expertise {
		id, err := entry.ID()
		require.NoError(t, err)
		ids[i] = dagql.NewID[*core.Expertise](id)
	}
	return ids
}

func messageTexts(llm *core.LLM) []string {
	var texts []string
	for _, msg := range llm.Messages {
		texts = append(texts, msg.TextContent())
	}
	return texts
}

// The fixture entries have no workspace; integration tests cover looking
// entries up again in a bound workspace before composing them.
func TestComposeRecordsAndRefreshes(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	var entry dagql.ObjectResult[*core.Expertise]
	require.NoError(t, srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: "fixtureExpertise"}))
	entryID, err := entry.ID()
	require.NoError(t, err)
	assertComposed := func(llm *core.LLM) {
		t.Helper()
		require.Len(t, llm.Expertise, 1)
		require.Empty(t, llm.ComposingExpertise)
		require.Len(t, llm.Messages, 1, "a refreshed entry replaces its contributions")
		require.Equal(t, "fixture", llm.Messages[0].CompositionOwner, "the entry's module owns its contribution")
	}

	// Empty is a no-op, including composing an empty record again.
	unchanged := composeExpertise(t, ctx, srv, seed)
	require.Empty(t, unchanged.Self().Expertise)
	require.Empty(t, unchanged.Self().Messages)
	unchanged = composeExpertise(t, ctx, srv, seed, recordIDs(t, seed)...)
	require.Empty(t, unchanged.Self().Messages)
	require.Zero(t, runs, "nothing to run")

	// Duplicates are recorded and run once rather than failing.
	id := dagql.NewID[*core.Expertise](entryID)
	composed := composeExpertise(t, ctx, srv, seed, id, id)
	assertComposed(composed.Self())
	require.Equal(t, 1, runs, "a repeated entry is recorded and run once")

	refreshed := composeExpertise(t, ctx, srv, composed, recordIDs(t, composed)...)
	assertComposed(refreshed.Self())
	require.Equal(t, 2, runs, "composing the record reruns every recorded entry")

	refreshed = composeExpertise(t, ctx, srv, refreshed, id)
	assertComposed(refreshed.Self())
	require.Equal(t, 3, runs, "composing a recorded entry refreshes it once, in place")
}

// Composing an entry already recorded refreshes its whole module as one
// batch: the module's contributions are cleared before any of its entries
// runs, then all of its recorded entries run again in record order, keeping
// their record positions whatever order they are given in. Other modules'
// entries are untouched.
func TestComposeRefreshesModuleAsOneBatch(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	entryID := func(field string) dagql.ID[*core.Expertise] {
		var entry dagql.ObjectResult[*core.Expertise]
		require.NoError(t, srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: field}))
		id, err := entry.ID()
		require.NoError(t, err)
		return dagql.NewID[*core.Expertise](id)
	}
	observing, contribute := entryID("fixtureObservingExpertise"), entryID("fixtureExpertise")
	other := fixtureEntryID(t, ctx, srv, "otherContribute")
	identities := func(llm dagql.ObjectResult[*core.LLM]) []string {
		var keys []string
		for _, entry := range llm.Self().Expertise {
			key, err := entry.Self().Identity()
			require.NoError(t, err)
			keys = append(keys, key)
		}
		return keys
	}

	composed := composeExpertise(t, ctx, srv, seed, observing, other, contribute)
	require.Equal(t, []string{"observed 0 messages", "other prompt", "fixture prompt"}, messageTexts(composed.Self()))
	record := identities(composed)
	require.Len(t, record, 3)
	require.Equal(t, 3, runs)

	// The observing entry runs first and sees only the other module's
	// prompt: its sibling's old contribution was cleared before it ran.
	refreshed := composeExpertise(t, ctx, srv, composed, observing, contribute)
	require.Equal(t, []string{"other prompt", "observed 1 messages", "fixture prompt"}, messageTexts(refreshed.Self()))
	require.Equal(t, record, identities(refreshed))
	require.Equal(t, 5, runs, "the other module's entry does not run again")

	// Giving one entry refreshes its whole module, in record order.
	for _, given := range []dagql.ID[*core.Expertise]{contribute, observing} {
		reordered := composeExpertise(t, ctx, srv, composed, given)
		require.Equal(t, []string{"other prompt", "observed 1 messages", "fixture prompt"}, messageTexts(reordered.Self()))
		require.Equal(t, record, identities(reordered))
	}

	// Composing every recorded entry refreshes every module.
	all := composeExpertise(t, ctx, srv, composed, recordIDs(t, composed)...)
	require.Equal(t, []string{"observed 0 messages", "other prompt", "fixture prompt"}, messageTexts(all.Self()))
	require.Equal(t, record, identities(all))
}

// A new entry alone does not refresh its module: it runs after the existing
// contributions and is appended to the record. Composing the whole record
// later refreshes all of the module's entries together.
func TestComposeNewEntryDoesNotRefreshModule(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	contribute, observing := fixtureEntryID(t, ctx, srv, "contribute"), fixtureEntryID(t, ctx, srv, "contributeObserving")

	composed := composeExpertise(t, ctx, srv, seed, contribute)
	promoted := composeExpertise(t, ctx, srv, composed, observing)
	require.Equal(t, []string{"fixture prompt", "observed 1 messages"}, messageTexts(promoted.Self()))
	require.Len(t, promoted.Self().Expertise, 2)
	require.Equal(t, 2, runs, "the recorded sibling does not run again")

	reloaded := composeExpertise(t, ctx, srv, promoted, recordIDs(t, promoted)...)
	require.Equal(t, []string{"fixture prompt", "observed 1 messages"}, messageTexts(reloaded.Self()))
	require.Len(t, reloaded.Self().Expertise, 2)
	require.Equal(t, 4, runs)
}

// Without an owning agent, an entry requiring one is skipped: it neither runs
// nor is recorded. An optional Agent argument receives null.
func TestComposeUnownedAgentArguments(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	require.Empty(t, seed.Self().OwnerAgentHandle)
	entryID := func(optional bool) dagql.ID[*core.Expertise] {
		var entry dagql.ObjectResult[*core.Expertise]
		require.NoError(t, srv.Select(ctx, srv.Root(), &entry, dagql.Selector{Field: "fixtureAgentExpertise", Args: []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(optional)}}}))
		id, err := entry.ID()
		require.NoError(t, err)
		return dagql.NewID[*core.Expertise](id)
	}
	required, optional := entryID(false), entryID(true)

	skipped := composeExpertise(t, ctx, srv, seed, required)
	require.Zero(t, runs, "a required-Agent entry does not run without an owner")
	require.Empty(t, skipped.Self().Expertise, "a skipped entry is not recorded")
	require.Empty(t, skipped.Self().Messages)

	composed := composeExpertise(t, ctx, srv, seed, required, optional)
	require.Equal(t, 1, runs, "only the optional-Agent entry runs")
	require.Len(t, composed.Self().Expertise, 1)
	require.Equal(t, []string{"maybe agent: null"}, messageTexts(composed.Self()))

	composed = composeExpertise(t, ctx, srv, composed, recordIDs(t, composed)...)
	require.Equal(t, 2, runs)
	require.Len(t, composed.Self().Expertise, 1)
	require.Equal(t, []string{"maybe agent: null"}, messageTexts(composed.Self()))
}

// A recorded entry requiring an Agent, composed again into a conversation
// that has no owner (e.g. by the reload pattern), keeps its record position
// and contributions instead of being stripped: a worker stays a worker. Its
// module cannot rerun in full, so it is not refreshed at all: only the
// module's new, runnable entries run.
func TestComposeKeepsRecordedEntrySkippedWithoutOwner(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	var required, optional dagql.ObjectResult[*core.Expertise]
	require.NoError(t, srv.Select(ctx, srv.Root(), &required, dagql.Selector{Field: "fixtureAgentExpertise", Args: []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}}}))
	require.NoError(t, srv.Select(ctx, srv.Root(), &optional, dagql.Selector{Field: "fixtureAgentExpertise", Args: []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}}}))
	requiredKey, err := required.Self().Identity()
	require.NoError(t, err)
	requiredID, err := required.ID()
	require.NoError(t, err)
	optionalID, err := optional.ID()
	require.NoError(t, err)

	// The state an owned composition leaves: the required entry recorded, its
	// module owning its contribution. Its owner is then gone.
	var recorded dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, seed, &recorded,
		dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{
			{Name: "prompt", Value: dagql.String("worker role")},
			{Name: "owner", Value: dagql.Opt(dagql.String("fixture"))},
		}},
		dagql.Selector{Field: "__withExpertise", Args: []dagql.NamedInput{{Name: "expertise", Value: dagql.ArrayInput[dagql.ID[*core.Expertise]]{dagql.NewID[*core.Expertise](requiredID)}}}},
	))
	require.Empty(t, recorded.Self().OwnerAgentHandle)

	// Composing the whole record, plus a new entry, leaves the required entry
	// in place: not cleared, not rerun, still first in the record.
	composed := composeExpertise(t, ctx, srv, recorded, append(recordIDs(t, recorded), dagql.NewID[*core.Expertise](optionalID))...)
	require.Equal(t, 1, runs, "only the new optional-Agent entry runs")
	require.Equal(t, []string{"worker role", "maybe agent: null"}, messageTexts(composed.Self()))
	require.Len(t, composed.Self().Expertise, 2)
	key, err := composed.Self().Expertise[0].Self().Identity()
	require.NoError(t, err)
	require.Equal(t, requiredKey, key)

	// Composing only the skipped recorded entry leaves the conversation as is.
	again := composeExpertise(t, ctx, srv, recorded, recordIDs(t, recorded)...)
	require.Equal(t, 1, runs)
	require.Equal(t, []string{"worker role"}, messageTexts(again.Self()))
	require.Len(t, again.Self().Expertise, 1)

	// So does refreshing the runnable sibling: rerunning it would require
	// clearing the module's skipped entry's contributions too.
	again = composeExpertise(t, ctx, srv, composed, dagql.NewID[*core.Expertise](optionalID))
	require.Equal(t, 1, runs)
	require.Equal(t, []string{"worker role", "maybe agent: null"}, messageTexts(again.Self()))
	require.Len(t, again.Self().Expertise, 2)
}

// A composition nested in a running entry never refreshes: composing a
// sibling of the running entry's module runs it once and records it, and
// refreshing the module later reruns both without recursing or duplicating.
func TestComposeNestedSibling(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	composer, contribute := fixtureEntryID(t, ctx, srv, "composeSibling"), fixtureEntryID(t, ctx, srv, "contribute")
	identities := func(llm dagql.ObjectResult[*core.LLM]) []string {
		var keys []string
		for _, entry := range llm.Self().Expertise {
			key, err := entry.Self().Identity()
			require.NoError(t, err)
			keys = append(keys, key)
		}
		return keys
	}
	composed := composeExpertise(t, ctx, srv, seed, composer)
	require.Equal(t, []string{"fixture prompt", "sibling composer"}, messageTexts(composed.Self()))
	want := identities(composed)
	require.Len(t, want, 2)
	require.True(t, strings.HasSuffix(want[0], "/composeSibling"), "the nested entry is recorded after its composer: %v", want)
	require.True(t, strings.HasSuffix(want[1], "/contribute"), "the nested entry is recorded after its composer: %v", want)
	require.Empty(t, composed.Self().ComposingExpertise)
	require.Equal(t, 2, runs)

	// The recipe replays cold, nested composition included.
	recipe, err := composed.RecipeID(ctx)
	require.NoError(t, err)
	replayCtx, replaySrv := newCompositionScopeSession(t, &runs)
	replayed, err := replaySrv.Load(replayCtx, recipe)
	require.NoError(t, err)
	restored, ok := dagql.UnwrapAs[*core.LLM](replayed)
	require.True(t, ok)
	require.Equal(t, messageTexts(composed.Self()), messageTexts(restored))
	require.Len(t, restored.Expertise, 2)
	require.Empty(t, restored.ComposingExpertise)
	runs = 2

	// Refreshing the module reruns the composer, whose nested compose now
	// finds its sibling recorded and leaves it to the refresh.
	for _, given := range [][]dagql.ID[*core.Expertise]{{composer}, recordIDs(t, composed)} {
		refreshed := composeExpertise(t, ctx, srv, composed, given...)
		require.Equal(t, []string{"sibling composer", "fixture prompt"}, messageTexts(refreshed.Self()))
		require.Equal(t, want, identities(refreshed))
		require.Empty(t, refreshed.Self().ComposingExpertise)
	}
	require.Equal(t, 4, runs)

	// Composed together, the sibling is being composed by the outer
	// composition, which runs it after its composer: the nested composition
	// leaves it alone rather than running it twice.
	runs = 0
	together := composeExpertise(t, ctx, srv, seed, composer, contribute)
	require.Equal(t, []string{"sibling composer", "fixture prompt"}, messageTexts(together.Self()))
	require.Equal(t, want, identities(together))
	require.Equal(t, 2, runs)
}

// An entry composing itself does not recurse: it is being composed.
func TestComposeNestedSelf(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	self := fixtureEntryID(t, ctx, srv, "composeSelf")
	composed := composeExpertise(t, ctx, srv, seed, self)
	require.Equal(t, []string{"self composer"}, messageTexts(composed.Self()))
	require.Len(t, composed.Self().Expertise, 1)
	refreshed := composeExpertise(t, ctx, srv, composed, self)
	require.Equal(t, []string{"self composer"}, messageTexts(refreshed.Self()))
	require.Len(t, refreshed.Self().Expertise, 1)
	require.Equal(t, 2, runs)
}

func messageOwners(llm *core.LLM) []string {
	var owners []string
	for _, msg := range llm.Messages {
		owners = append(owners, msg.CompositionOwner)
	}
	return owners
}

// A dependency's prompt, made while an entry ran, becomes the entry's
// module's: refreshing the module replaces it rather than adding it again
// beside the old one, and replay reproduces the claim.
func TestComposeClaimsDependencyContributions(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	entry := fixtureEntryID(t, ctx, srv, "contributeViaDependency")
	texts := []string{"donor prompt", "via dependency"}
	owners := []string{"fixture", "fixture"}

	composed := composeExpertise(t, ctx, srv, seed, entry)
	require.Equal(t, texts, messageTexts(composed.Self()))
	require.Equal(t, owners, messageOwners(composed.Self()))
	for range 2 {
		composed = composeExpertise(t, ctx, srv, composed, recordIDs(t, composed)...)
		require.Equal(t, texts, messageTexts(composed.Self()), "a refresh must not add the dependency's prompt again")
		require.Equal(t, owners, messageOwners(composed.Self()))
	}

	recipe, err := composed.RecipeID(ctx)
	require.NoError(t, err)
	replayCtx, replaySrv := newCompositionScopeSession(t, &runs)
	replayed, err := replaySrv.Load(replayCtx, recipe)
	require.NoError(t, err)
	restored, ok := dagql.UnwrapAs[*core.LLM](replayed)
	require.True(t, ok)
	require.Equal(t, texts, messageTexts(restored))
	require.Equal(t, owners, messageOwners(restored))
}

// An entry composing another module's entry does not claim that entry's
// contributions: they stay its module's, kept when the composer's module
// refreshes, since the nested composition then finds the entry recorded and
// does not run it again.
func TestComposeNestedOtherModuleKeepsContributions(t *testing.T) {
	runs := 0
	ctx, srv := newCompositionScopeSession(t, &runs)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}}))
	composer := fixtureEntryID(t, ctx, srv, "composeOther")
	texts := []string{"other prompt", "other composer"}
	owners := []string{"other", "fixture"}

	composed := composeExpertise(t, ctx, srv, seed, composer)
	require.Equal(t, texts, messageTexts(composed.Self()))
	require.Equal(t, owners, messageOwners(composed.Self()))
	require.Len(t, composed.Self().Expertise, 2)

	refreshed := composeExpertise(t, ctx, srv, composed, composer)
	require.Equal(t, texts, messageTexts(refreshed.Self()))
	require.Equal(t, owners, messageOwners(refreshed.Self()))
	require.Len(t, refreshed.Self().Expertise, 2)
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
