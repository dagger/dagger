package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

const persistedMetadataTestIntrospection = `{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"hello","args":[],"type":{"kind":"SCALAR","name":"String"},"directives":[{"name":"sourceMap","args":[{"name":"module","value":"\"m\""}]}]}]}],"directives":[{"name":"sourceMap","description":"where a type came from","locations":["FIELD_DEFINITION"],"args":[{"name":"module","type":{"kind":"SCALAR","name":"String"}}]}]}}`

// TestPersistedMetadataFamiliesSurviveRestart saves every ordinary metadata
// and report family through a restart, reads it back, compares its semantic
// fields with the fresh value, and saves it again from both the typed-read
// and the untouched middle process.
func TestPersistedMetadataFamiliesSurviveRestart(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "metadata-a")
	ctx, cache, srv := env.open(t)

	present := ""
	described := "web"
	schema, err := NewSchema(JSON(persistedMetadataTestIntrospection))
	require.NoError(t, err)
	schemaContents, err := schema.Contents()
	require.NoError(t, err)
	module := env.attach(t, ctx, cache, srv, "reflected-module", &Module{NameField: "reflected", OriginalName: "Reflected", Deps: NewSchemaBuilder(nil, nil)})
	moduleID := persistedRowID(t, cache, module)
	before := env.directory(t, ctx, cache, srv, "migration-before", "migration-before")
	after := env.directory(t, ctx, cache, srv, "migration-after", "migration-after")
	beforeID, afterID := persistedRowID(t, cache, before), persistedRowID(t, cache, after)
	changes, err := NewChangeset(ctx, before, after)
	require.NoError(t, err)
	stepChanges, err := NewChangeset(ctx, after, before)
	require.NoError(t, err)
	changesRes := env.attach(t, ctx, cache, srv, "migration-changes", changes).(dagql.ObjectResult[*Changeset])
	stepChangesRes := env.attach(t, ctx, cache, srv, "migration-step-changes", stepChanges).(dagql.ObjectResult[*Changeset])
	changesID := persistedRowID(t, cache, changesRes)
	stepChangesID := persistedRowID(t, cache, stepChangesRes)
	emptyChanges, err := NewChangeset(ctx, before, before)
	require.NoError(t, err)
	emptyChangesRes := env.attach(t, ctx, cache, srv, "migration-empty-changes", emptyChanges).(dagql.ObjectResult[*Changeset])

	type fixture struct {
		field string
		value dagql.Typed
		check func(t *testing.T, restored dagql.Typed)
	}
	fixtures := []fixture{
		{"env-var", EnvVariable{Name: "CI", Value: "true"}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, EnvVariable{Name: "CI", Value: "true"}, restored)
		}},
		{"env-var-empty", EnvVariable{Name: "EMPTY", Value: ""}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, EnvVariable{Name: "EMPTY", Value: ""}, restored)
		}},
		{"port-nil-description", Port{Port: 8080, Protocol: NetworkProtocolTCP}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, Port{Port: 8080, Protocol: NetworkProtocolTCP}, restored)
			require.Nil(t, restored.(Port).Description, "an absent description stays absent")
		}},
		{"port-empty-description", Port{Port: 443, Protocol: NetworkProtocolTCP, Description: &present}, func(t *testing.T, restored dagql.Typed) {
			require.NotNil(t, restored.(Port).Description, "an empty present description stays present")
			require.Equal(t, "", *restored.(Port).Description)
		}},
		{"port-described", Port{Port: 80, Protocol: NetworkProtocolTCP, Description: &described, ExperimentalSkipHealthcheck: true}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, Port{Port: 80, Protocol: NetworkProtocolTCP, Description: &described, ExperimentalSkipHealthcheck: true}, restored)
		}},
		{"sdk-config", &SDKConfig{
			Source: "go", Debug: true,
			Config: map[string]any{
				"big":    json.Number("9007199254740993"),
				"float":  json.Number("1.5"),
				"nested": map[string]any{"list": []any{json.Number("1"), "two", true}},
			},
			Experimental: map[string]bool{"x": true},
		}, func(t *testing.T, restored dagql.Typed) {
			cfg := restored.(*SDKConfig)
			require.Equal(t, "go", cfg.Source)
			require.True(t, cfg.Debug)
			require.Equal(t, json.Number("9007199254740993"), cfg.Config["big"], "untyped configuration keeps exact numbers")
			require.Equal(t, map[string]any{"list": []any{json.Number("1"), "two", true}}, cfg.Config["nested"])
			require.Equal(t, map[string]bool{"x": true}, cfg.Experimental)
		}},
		{"git-bundle-ref", &GitBundleRef{Name: "refs/heads/main", SHA: "0123abcd"}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, &GitBundleRef{Name: "refs/heads/main", SHA: "0123abcd"}, restored)
		}},
		{"schema", schema, func(t *testing.T, restored dagql.Typed) {
			contents, err := restored.(*Schema).Contents()
			require.NoError(t, err)
			require.JSONEq(t, string(schemaContents), string(contents), "the parsed introspection round-trips")
			require.Len(t, restored.(*Schema).Introspection.Schema.Directives, 1, "directive definitions survive")
			require.Equal(t, "sourceMap", restored.(*Schema).Introspection.Schema.Directives[0].Name)
		}},
		{"current-module", &CurrentModule{Module: module.(dagql.ObjectResult[*Module])}, func(t *testing.T, restored dagql.Typed) {
			current := restored.(*CurrentModule)
			require.Equal(t, moduleID, persistedRowID(t, cache, current.Module), "the exact retained module row")
			require.Equal(t, "reflected", current.Module.Self().NameField)
			require.Equal(t, "Reflected", current.Module.Self().OriginalName)
		}},
		{"workspace-migration", &WorkspaceMigration{
			Changes:          changesRes,
			ModuleCandidates: []string{"fixture", "module"},
			ConfigFile:       "dagger.toml",
			Steps: []*WorkspaceMigrationStep{
				{Code: "move-config", Description: "moves the config", Warnings: []string{"first", "second"}, Changes: stepChangesRes},
				{Code: "no-changes", Warnings: nil, Changes: emptyChangesRes},
			},
		}, func(t *testing.T, restored dagql.Typed) {
			migration := restored.(*WorkspaceMigration)
			require.Equal(t, changesID, persistedRowID(t, cache, migration.Changes))
			require.Equal(t, stepChangesID, persistedRowID(t, cache, migration.Steps[0].Changes))
			require.Equal(t, []string{"fixture", "module"}, migration.ModuleCandidates)
			require.Equal(t, "dagger.toml", migration.ConfigFile)
			require.Equal(t, beforeID, persistedRowID(t, cache, migration.Changes.Self().Before))
			require.Equal(t, afterID, persistedRowID(t, cache, migration.Changes.Self().After))
			require.Len(t, migration.Steps, 2)
			require.Equal(t, "move-config", migration.Steps[0].Code)
			require.Equal(t, "moves the config", migration.Steps[0].Description)
			require.Equal(t, []string{"first", "second"}, migration.Steps[0].Warnings, "warning order survives")
			require.Equal(t, afterID, persistedRowID(t, cache, migration.Steps[0].Changes.Self().Before), "the step's own changeset is kept apart from the report's")
			require.Equal(t, beforeID, persistedRowID(t, cache, migration.Steps[0].Changes.Self().After))
			require.Equal(t, "no-changes", migration.Steps[1].Code)
			require.Equal(t, beforeID, persistedRowID(t, cache, migration.Steps[1].Changes.Self().Before))
			require.Equal(t, beforeID, persistedRowID(t, cache, migration.Steps[1].Changes.Self().After), "a step without changes keeps identical endpoints")
		}},
		{"workspace-migration-step", &WorkspaceMigrationStep{Code: "solo", Warnings: []string{"w"}, Changes: changesRes}, func(t *testing.T, restored dagql.Typed) {
			step := restored.(*WorkspaceMigrationStep)
			require.Equal(t, "solo", step.Code)
			require.Equal(t, []string{"w"}, step.Warnings)
			require.Equal(t, beforeID, persistedRowID(t, cache, step.Changes.Self().Before))
		}},
		{"cloud", &Cloud{}, func(t *testing.T, restored dagql.Typed) { require.Equal(t, &Cloud{}, restored) }},
		{"terminal-legacy", &TerminalLegacy{}, func(t *testing.T, restored dagql.Typed) { require.Equal(t, &TerminalLegacy{}, restored) }},
		{"module-config-client", &modules.ModuleConfigClient{Generator: "go", Directory: "./gen"}, func(t *testing.T, restored dagql.Typed) {
			require.Equal(t, &modules.ModuleConfigClient{Generator: "go", Directory: "./gen"}, restored)
		}},
	}

	type saved struct {
		id       uint64
		typeName string
		encoding []byte
	}
	rows := map[string]saved{}
	for _, f := range fixtures {
		res := env.attach(t, ctx, cache, srv, f.field, f.value)
		rows[f.field] = saved{id: persistedRowID(t, cache, res), typeName: res.Type().Name(), encoding: persistedEncodingBytes(t, ctx, cache, res)}
	}
	// The reference-bearing families declare exactly the rows they own.
	for _, field := range []string{"current-module", "workspace-migration", "workspace-migration-step"} {
		res, err := cache.LoadResultByResultID(ctx, env.session, srv, rows[field].id)
		require.NoError(t, err)
		refs := assertPersistedRefsMatchOwnership(t, ctx, cache, res)
		require.NotEmpty(t, refs)
	}
	for _, field := range []string{"env-var", "port-described", "sdk-config", "schema", "cloud", "module-config-client"} {
		res, err := cache.LoadResultByResultID(ctx, env.session, srv, rows[field].id)
		require.NoError(t, err)
		require.Empty(t, persistedVisitedRefs(t, ctx, cache, res), "%s declares no references", field)
	}

	check := func(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, round string) {
		t.Helper()
		for _, f := range fixtures {
			t.Run(round+"/"+f.field, func(t *testing.T) {
				restored, err := cache.LoadResultByResultID(ctx, env.session, srv, rows[f.field].id)
				require.NoError(t, err)
				require.Equal(t, rows[f.field].typeName, restored.Type().Name())
				f.check(t, restored.Unwrap())
				require.Equal(t, rows[f.field].encoding, persistedEncodingBytes(t, ctx, cache, restored), "the second save is byte-identical")
			})
		}
	}

	// Typed-read middle process.
	ctx, cache, srv = env.restart(t, ctx, cache)
	check(t, ctx, cache, srv, "first-restart")
	// Untouched middle process: nothing is read before the next save.
	ctx, cache, srv = env.restart(t, ctx, cache)
	ctx, cache, srv = env.restart(t, ctx, cache)
	check(t, ctx, cache, srv, "after-untouched-save")
}

// TestPersistedMetadataControlsDetectLoss shows the comparisons above are
// sensitive: dropping an optional present field, an ordered member or exact
// number precision produces an observable difference.
func TestPersistedMetadataControlsDetectLoss(t *testing.T) {
	ctx := context.Background()
	dec := dagql.NewPersistDecodeContext(nil, 0, nil)

	t.Run("dropping a present empty port description", func(t *testing.T) {
		present := ""
		encoded, err := Port{Port: 1, Protocol: NetworkProtocolTCP, Description: &present}.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
		require.Contains(t, payload, "description")
		delete(payload, "description")
		lossy, err := json.Marshal(payload)
		require.NoError(t, err)
		restored, err := Port{}.DecodePersistedObject(ctx, dec, lossy)
		require.NoError(t, err)
		require.Nil(t, restored.(Port).Description, "the loss is observable")
		exact, err := Port{}.DecodePersistedObject(ctx, dec, encoded.JSON)
		require.NoError(t, err)
		require.NotNil(t, exact.(Port).Description)
	})
	t.Run("reordering migration warnings and steps", func(t *testing.T) {
		step := &WorkspaceMigrationStep{Code: "a", Warnings: []string{"first", "second"}}
		encoded, err := step.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
		payload["warnings"] = []any{"second", "first"}
		reordered, err := json.Marshal(payload)
		require.NoError(t, err)
		restored, err := (&WorkspaceMigrationStep{}).DecodePersistedObject(ctx, dec, reordered)
		require.NoError(t, err)
		require.NotEqual(t, step.Warnings, restored.(*WorkspaceMigrationStep).Warnings)
	})
	t.Run("untyped number decoding rounds the SDK config", func(t *testing.T) {
		cfg := &SDKConfig{Config: map[string]any{"big": json.Number("9007199254740993")}}
		encoded, err := cfg.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		var lossy persistedSDKConfigPayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &lossy))
		require.NotEqual(t, cfg.Config["big"], lossy.Config["big"], "encoding/json without UseNumber cannot keep the value")
		restored, err := (&SDKConfig{}).DecodePersistedObject(ctx, dec, encoded.JSON)
		require.NoError(t, err)
		require.Equal(t, cfg.Config["big"], restored.(*SDKConfig).Config["big"])
	})
	t.Run("sdk config keeps empty maps distinct from absent ones", func(t *testing.T) {
		// A module source builds an empty Experimental map on purpose when
		// it clears every feature, so the payload must not collapse it to
		// nil. Config is plain data the type does not normalize either.
		for _, tc := range []struct {
			name string
			cfg  *SDKConfig
		}{
			{"absent maps", &SDKConfig{Source: "go"}},
			{"empty maps", &SDKConfig{Source: "go", Config: map[string]any{}, Experimental: map[string]bool{}}},
			{"present maps", &SDKConfig{Source: "go", Config: map[string]any{"k": "v"}, Experimental: map[string]bool{"f": true}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				encoded, err := tc.cfg.EncodePersistedObject(ctx, nil)
				require.NoError(t, err)
				restoredTyped, err := (&SDKConfig{}).DecodePersistedObject(ctx, dec, encoded.JSON)
				require.NoError(t, err)
				restored := restoredTyped.(*SDKConfig)
				require.Equal(t, tc.cfg.Config == nil, restored.Config == nil, "an absent map stays absent and an empty one stays empty")
				require.Equal(t, tc.cfg.Experimental == nil, restored.Experimental == nil)
				require.Equal(t, tc.cfg.Config, restored.Config)
				require.Equal(t, tc.cfg.Experimental, restored.Experimental)
				reencoded, err := restored.EncodePersistedObject(ctx, nil)
				require.NoError(t, err)
				require.Equal(t, string(encoded.JSON), string(reencoded.JSON), "the second save writes the same shape")
			})
		}
	})
	t.Run("current module without its row is absent", func(t *testing.T) {
		restored, err := (&CurrentModule{}).DecodePersistedObject(ctx, dec, json.RawMessage(`{}`))
		require.NoError(t, err)
		require.Nil(t, restored.(*CurrentModule).Module.Self())
	})
}

// TestPersistedOptionalVoidSurvivesRestart covers the supported Void route.
// The VOID kind documents that its outer TypeDef is always optional and that
// a Void is never actually represented, and every official SDK declares void
// returns optional, so a retained void return is an attached absent value
// under a nullable Void declaration and is carried by the generic null codec
// with its row identity. The primitive converter would return a present Void
// for a hand-built non-optional declaration; that is outside the documented
// contract and gets no codec of its own here.
func TestPersistedOptionalVoidSurvivesRestart(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "void-a")
	ctx, cache, srv := env.open(t)

	voidDef := (&TypeDef{}).WithKind(TypeDefKindVoid).WithOptional(true)
	require.True(t, voidDef.Optional)
	require.Equal(t, "Void", voidDef.ToTyped().Type().String(), "an optional VOID declaration is a nullable Void")
	primitive := &PrimitiveType{Def: voidDef.WithOptional(false)}
	voidDefRes := newTypeDefDetachedResult(t, srv, "void-typedef", voidDef.WithOptional(false))
	nullable := &NullableType{InnerDef: voidDefRes, Inner: primitive}
	absentFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "void-absent", Type: &dagql.ResultCallType{NamedType: "Void"}}
	absentRes, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{ResultCall: absentFrame, IsPersistable: true}, func(ctx context.Context) (dagql.AnyResult, error) {
		return nullable.ConvertFromSDKResult(dagql.ContextWithCall(ctx, absentFrame), nil)
	})
	require.NoError(t, err)
	_, present := absentRes.DerefValue()
	require.False(t, present, "the optional route converts a nil SDK result to an attached absent value")
	require.Equal(t, "Void", absentRes.Type().String())
	absentID := persistedRowID(t, cache, absentRes)
	absentEncoding := persistedEncoding(t, ctx, cache, absentRes)
	require.Equal(t, "null", absentEncoding.Envelope.Kind)
	require.Equal(t, absentID, absentEncoding.Envelope.ResultID, "the absent void keeps its row identity")
	freshType := absentRes.Type().String()

	for round := 1; round <= 2; round++ {
		ctx, cache, srv = env.restart(t, ctx, cache)
		restored, err := cache.LoadResultByResultID(ctx, env.session, srv, absentID)
		require.NoError(t, err)
		require.Equal(t, absentID, persistedRowID(t, cache, restored))
		_, present := restored.DerefValue()
		require.False(t, present, "round %d: restored absence dereferences to nothing", round)
		require.Equal(t, freshType, restored.Type().String(), "round %d: the restored view matches the fresh nullable Void", round)
		require.Equal(t, absentEncoding.Envelope, persistedEncoding(t, ctx, cache, restored).Envelope, "round %d: the second save is identical", round)
	}

	t.Run("a nil result under a non-optional declaration is a present Void outside the contract", func(t *testing.T) {
		presentFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "void-present", Type: dagql.NewResultCallType(Void{}.Type())}
		res, err := primitive.ConvertFromSDKResult(dagql.ContextWithCall(ctx, presentFrame), nil)
		require.NoError(t, err)
		require.Equal(t, dagql.Typed(Void{}), res.Unwrap())
		require.Equal(t, "Void!", res.Type().String())
		raw, err := json.Marshal(Void{})
		require.NoError(t, err)
		require.JSONEq(t, "{}", string(raw), "the fresh JSON form of a present Void is the empty object; its literal form is null")
		require.Equal(t, "null", Void{}.ToLiteral().ToAST().String())
		_, err = Void{}.DecodeInput(map[string]any{})
		require.Error(t, err, "a Void cannot be constructed from any input; this route has no readable representation and no codec is added for it")
	})
}

// TestPersistedModuleConfigurationNumbersStayExact covers the untyped
// configuration maps that a module and its source carry alongside their
// declared fields: Module.SDKConfig.Config, Module.WorkspaceConfig and
// ModuleSource's own SDK configuration. These are the values that reach an
// SDK as configuration after a restart, so a number that rounds through
// float64 in their payloads changes the configuration the module is given.
func TestPersistedModuleConfigurationNumbersStayExact(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "module-config-a")
	ctx, cache, srv := env.open(t)
	enc := dagql.NewPersistEncodeContext(cache, 0, nil)
	dec := dagql.NewPersistDecodeContext(srv, 0, nil)

	const bigInt = "9007199254740993" // 2^53+1: the first integer float64 cannot hold
	const minInt64 = "-9223372036854775808"
	nested := func() map[string]any {
		return map[string]any{
			"limits": map[string]any{"max": json.Number(bigInt), "floor": json.Number(minInt64)},
			"ports":  []any{json.Number("8080"), json.Number(bigInt)},
			"ratio":  json.Number("0.1"),
		}
	}

	assertNested := func(t *testing.T, label string, cfg map[string]any) {
		t.Helper()
		limits, ok := cfg["limits"].(map[string]any)
		require.True(t, ok, label)
		require.Equal(t, json.Number(bigInt), limits["max"], "%s: nested large integer is exact", label)
		require.Equal(t, json.Number(minInt64), limits["floor"], label)
		ports, ok := cfg["ports"].([]any)
		require.True(t, ok, label)
		require.Equal(t, json.Number(bigInt), ports[1], "%s: a list position is exact too", label)
		require.Equal(t, json.Number("0.1"), cfg["ratio"], label)
	}

	t.Run("module SDK and workspace configuration", func(t *testing.T) {
		mod := &Module{
			NameField:       "cfg",
			SDKConfig:       &SDKConfig{Source: "go", Config: nested()},
			WorkspaceConfig: nested(),
		}
		encoded, err := mod.EncodePersistedObject(ctx, enc)
		require.NoError(t, err)
		payload := encoded.JSON
		for round := 1; round <= 2; round++ {
			restoredTyped, err := (&Module{}).DecodePersistedObject(ctx, dec, payload)
			require.NoError(t, err)
			restored := restoredTyped.(*Module)
			assertNested(t, "sdk config", restored.SDKConfig.Config)
			assertNested(t, "workspace config", restored.WorkspaceConfig)
			reencoded, err := restored.EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			// Exact bytes: require.JSONEq would read both sides with an
			// ordinary json.Unmarshal and round these integers.
			require.Equal(t, string(payload), string(reencoded.JSON), "round %d: the second save writes the same tokens", round)
			payload = reencoded.JSON
		}

		// Deliberate control: the same bytes read with the ordinary untyped
		// decoder, which the payload used before it adopted a lossless
		// reader, round the nested integer.
		var lossy persistedModulePayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &lossy))
		lossyLimits, ok := lossy.WorkspaceConfig["limits"].(map[string]any)
		require.True(t, ok)
		require.NotEqual(t, json.Number(bigInt), lossyLimits["max"], "untyped decoding must lose the value")
	})

	t.Run("module source SDK configuration", func(t *testing.T) {
		src := &ModuleSource{SDK: &SDKConfig{Source: "go", Config: nested()}, SDKImpl: &moduleSourceSelfCallsTestSDK{}}
		encoded, err := src.EncodePersistedObject(ctx, enc)
		require.NoError(t, err)
		payload := encoded.JSON
		for round := 1; round <= 2; round++ {
			restoredTyped, err := (&ModuleSource{}).DecodePersistedObject(ctx, dec, payload)
			require.NoError(t, err)
			restored := restoredTyped.(*ModuleSource)
			require.NotNil(t, restored.SDK)
			assertNested(t, "module source sdk config", restored.SDK.Config)
			reencoded, err := restored.EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			// Exact bytes: require.JSONEq would read both sides with an
			// ordinary json.Unmarshal and round these integers.
			require.Equal(t, string(payload), string(reencoded.JSON), "round %d: the second save writes the same tokens", round)
			payload = reencoded.JSON
		}

		var lossy persistedModuleSourcePayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &lossy))
		require.NotNil(t, lossy.SDK)
		lossyLimits, ok := lossy.SDK.Config["limits"].(map[string]any)
		require.True(t, ok)
		require.NotEqual(t, json.Number(bigInt), lossyLimits["max"], "untyped decoding must lose the value")
	})
}
