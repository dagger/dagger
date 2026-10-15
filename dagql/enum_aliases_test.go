package dagql_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type aliasArch string

var aliasArchs = dagql.NewEnum[aliasArch]()

var (
	aliasArchARM64 = aliasArchs.Register("ARM64")
	aliasArchAMD64 = aliasArchs.Register("AMD64")
	aliasArchX86   = aliasArchs.Register("X86")
	aliasArchRISCV = aliasArchs.Register("RISCV")
)

var (
	_ = aliasArchs.WithAliases(aliasArchARM64, aliasesOf("ARM_64", "AARCH_64"))
	// "X86" is a real value, "OLD" is claimed by RISCV too.
	_ = aliasArchs.WithAliases(aliasArchAMD64, aliasesOf("AMD_64", "X86", "OLD"))
	// Only accepted in view v1.
	_ = aliasArchs.WithAliases(aliasArchRISCV, dagql.Aliases{Names: []string{"OLD", "RISC_V"}, View: dagql.ExactView("v1")})
)

func (aliasArch) Decoder() dagql.InputDecoder {
	return aliasArchs
}

func (a aliasArch) ToLiteral() call.Literal {
	return aliasArchs.Literal(a)
}

func (aliasArch) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasArch", NonNull: true}
}

type aliasPlatform struct {
	Arch aliasArch
}

func (aliasPlatform) TypeName() string {
	return "AliasPlatform"
}

type aliasBuild struct {
	Archs string `field:"true"`
}

func (*aliasBuild) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasBuild", NonNull: true}
}

// newEnumAliasTestServer serves Query.build(arch: AliasArch!, archs:
// [AliasArch!], platform: AliasPlatform): AliasBuild, and
// Query.defaultArch: AliasArch!, with the hidden value aliases of
// aliasArchs. It counts the calls to build.
func newEnumAliasTestServer(t *testing.T) (context.Context, *dagql.Server, *atomic.Int32) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:              "dagql-enum-alias-client",
		SessionID:             "dagql-enum-alias-session",
		UseRecipeIDsByDefault: true,
	})
	ctx = dagql.ContextWithCache(ctx, newCache(t))

	srv := newExternalDagqlServerForTest(t, Query{})
	introspection.Install[Query](srv)
	aliasArchs.Install(srv)
	dagql.MustInputSpec(aliasPlatform{}).Install(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*aliasBuild]{}))

	var calls atomic.Int32
	dagql.Fields[Query]{
		dagql.Func("build", func(ctx context.Context, self Query, args struct {
			Arch     aliasArch
			Archs    dagql.Optional[dagql.ArrayInput[aliasArch]]
			Platform dagql.Optional[dagql.InputObject[aliasPlatform]]
		}) (*aliasBuild, error) {
			calls.Add(1)
			archs := []string{string(args.Arch)}
			if args.Archs.Valid {
				for _, arch := range args.Archs.Value {
					archs = append(archs, string(arch))
				}
			}
			if args.Platform.Valid {
				archs = append(archs, string(args.Platform.Value.Value.Arch))
			}
			return &aliasBuild{Archs: strings.Join(archs, ",")}, nil
		}),
		dagql.Func("defaultArch", func(ctx context.Context, self Query, _ struct{}) (aliasArch, error) {
			return aliasArchARM64, nil
		}),
	}.Install(srv)
	dagql.Fields[*aliasBuild]{}.Install(srv)
	return ctx, srv, &calls
}

func enumAliasQuery(t *testing.T, ctx context.Context, srv *dagql.Server, query string, vars map[string]any) (map[string]any, error) {
	t.Helper()
	data, err := srv.Query(ctx, query, vars)
	if err != nil {
		return nil, err
	}
	return roundTrip(t, data), nil
}

// An enum value alias decodes to its canonical value, given as an inline
// literal, in a list, in an input object or as a variable.
func TestEnumValueAliasesQuery(t *testing.T) {
	ctx, srv, _ := newEnumAliasTestServer(t)

	data, err := enumAliasQuery(t, ctx, srv, `{
		build(arch: ARM_64, archs: [AMD_64, ARM64], platform: {arch: AARCH_64}) { archs }
	}`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"build": map[string]any{"archs": "ARM64,AMD64,ARM64,ARM64"}}, data)

	data, err = enumAliasQuery(t, ctx, srv, `query($arch: AliasArch!, $archs: [AliasArch!], $platform: AliasPlatform) {
		build(arch: $arch, archs: $archs, platform: $platform) { archs }
	}`, map[string]any{
		"arch":     "AMD_64",
		"archs":    []any{"ARM_64"},
		"platform": map[string]any{"arch": "AMD_64"},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"build": map[string]any{"archs": "AMD64,ARM64,AMD64"}}, data)

	// Through gqlgen's handler too, which validates variables by itself.
	var res struct {
		Build struct{ Archs string }
	}
	req(t, newTestClient(srv), `{ build(arch: ARM_64) { archs } }`, &res)
	require.Equal(t, "ARM64", res.Build.Archs)
	require.NoError(t, newTestClient(srv).Post(`query($arch: AliasArch!) { build(arch: $arch) { archs } }`, &res, client.Var("arch", "AMD_64")))
	require.Equal(t, "AMD64", res.Build.Archs)

	// Outputs have the canonical name.
	data, err = enumAliasQuery(t, ctx, srv, `{ defaultArch }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"defaultArch": "ARM64"}, data)

	// Unknown values still fail.
	_, err = enumAliasQuery(t, ctx, srv, `{ build(arch: ARM_65) { archs } }`, nil)
	require.ErrorContains(t, err, `Value "ARM_65" does not exist in "AliasArch!" enum`)
}

// The enum's decoder accepts the aliases, wherever the value comes from: a
// CLI flag or a JSON document is a string.
func TestEnumValueAliasesDecode(t *testing.T) {
	for alias, canonical := range map[string]aliasArch{
		"ARM_64":   aliasArchARM64,
		"AARCH_64": aliasArchARM64,
		"AMD_64":   aliasArchAMD64,
		"RISC_V":   aliasArchRISCV, // decoders have no view
		"X86":      aliasArchX86,   // a real value wins
		"ARM64":    aliasArchARM64,
	} {
		decoded, err := aliasArchs.DecodeInput(alias)
		require.NoError(t, err, alias)
		require.Equal(t, canonical, decoded, alias)
		require.Equal(t, call.NewLiteralEnum(string(canonical)), decoded.ToLiteral(), alias)
	}
	_, err := aliasArchs.DecodeInput("OLD")
	require.ErrorContains(t, err, `invalid enum member "OLD"`, "two values claim it")

	decoded, err := dagql.ArrayInput[aliasArch]{}.DecodeInput([]any{"ARM_64", "AMD64"})
	require.NoError(t, err)
	require.Equal(t, dagql.ArrayInput[aliasArch]{aliasArchARM64, aliasArchAMD64}, decoded)
}

// A query using an enum value alias makes the canonical query's calls:
// same IDs, same cache entries.
func TestEnumValueAliasesCanonicalCalls(t *testing.T) {
	ctx, srv, calls := newEnumAliasTestServer(t)

	id := func(query string, vars map[string]any) string {
		t.Helper()
		data, err := srv.Query(ctx, query, vars)
		require.NoError(t, err)
		var res struct {
			Build struct{ ID string }
		}
		require.NoError(t, json.Unmarshal(mustJSON(t, data), &res))
		require.NotEmpty(t, res.Build.ID)
		return res.Build.ID
	}

	canonical := id(`{ build(arch: ARM64, archs: [AMD64], platform: {arch: AMD64}) { id } }`, nil)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, canonical, id(`{ build(arch: ARM_64, archs: [AMD_64], platform: {arch: AMD_64}) { id } }`, nil))
	require.Equal(t, canonical, id(`query($arch: AliasArch!, $archs: [AliasArch!], $platform: AliasPlatform) {
		build(arch: $arch, archs: $archs, platform: $platform) { id }
	}`, map[string]any{
		"arch":     "AARCH_64",
		"archs":    []any{"AMD_64"},
		"platform": map[string]any{"arch": "AMD64"},
	}))
	require.Equal(t, int32(1), calls.Load(), "aliased queries should hit the canonical call's cache entry")
}

// Nothing a client can read shows the enum value aliases.
func TestEnumValueAliasesHidden(t *testing.T) {
	ctx, srv, _ := newEnumAliasTestServer(t)

	values := func(def *ast.Definition) []string {
		var names []string
		for _, value := range def.EnumValues {
			names = append(names, value.Name)
		}
		return names
	}
	require.Equal(t, []string{"ARM64", "AMD64", "X86", "RISCV"}, values(srv.Schema().Types["AliasArch"]))
	require.Equal(t, []string{"ARM64", "AMD64", "X86", "RISCV"}, values(srv.SchemaForView(srv.View).Types["AliasArch"]))

	data, err := srv.Query(ctx, `{ __type(name: "AliasArch") { enumValues(includeDeprecated: true) { name } } }`, nil)
	require.NoError(t, err)
	raw := string(mustJSON(t, data))
	for _, alias := range []string{"ARM_64", "AARCH_64", "AMD_64"} {
		require.NotContains(t, raw, `"`+alias+`"`)
	}
	require.Contains(t, raw, `"ARM64"`)

	// The schema queries are validated against has them, deprecated.
	aliased := srv.SchemaWithAliases().Types["AliasArch"]
	require.Equal(t, []string{"ARM64", "AARCH_64", "ARM_64", "AMD64", "AMD_64", "X86", "RISCV"}, values(aliased))
	require.Equal(t, "Use `ARM64` instead.", deprecationReason(aliased.EnumValues.ForName("ARM_64").Directives))
	require.Equal(t, "<not deprecated>", deprecationReason(aliased.EnumValues.ForName("ARM64").Directives))
	require.Equal(t, []string{"ARM64", "AMD64", "X86", "RISCV"}, values(srv.Schema().Types["AliasArch"]), "the schema itself didn't change")
}

// Enum value aliases limited to some views are accepted only in those, and
// an alias that collides with a value, or that two values claim, is
// ignored.
func TestEnumValueAliasesViewsAndCollisions(t *testing.T) {
	ctx, srv, _ := newEnumAliasTestServer(t)

	srv.View = "v0"
	_, err := enumAliasQuery(t, ctx, srv, `{ build(arch: RISC_V) { archs } }`, nil)
	require.ErrorContains(t, err, `Value "RISC_V" does not exist`)
	_, err = enumAliasQuery(t, ctx, srv, `{ build(arch: OLD) { archs } }`, nil)
	require.ErrorContains(t, err, `Value "OLD" does not exist`, "OLD is claimed by two values, if in different views")

	srv.View = "v1"
	data, err := enumAliasQuery(t, ctx, srv, `{ build(arch: RISC_V) { archs } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"build": map[string]any{"archs": "RISCV"}}, data)
	_, err = enumAliasQuery(t, ctx, srv, `{ build(arch: OLD) { archs } }`, nil)
	require.ErrorContains(t, err, `Value "OLD" does not exist`)

	// X86 is AMD64's alias, but it's a real value.
	data, err = enumAliasQuery(t, ctx, srv, `{ build(arch: X86) { archs } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"build": map[string]any{"archs": "X86"}}, data)
	x86 := srv.SchemaWithAliases().Types["AliasArch"].EnumValues.ForName("X86")
	require.Equal(t, "<not deprecated>", deprecationReason(x86.Directives))
}

// Enum value aliases are part of the schema digest: they change what it
// accepts.
func TestEnumValueAliasesSchemaDigest(t *testing.T) {
	without := newExternalDagqlServerForTest(t, Query{})
	dagql.NewEnum[aliasArch]("ARM64", "AMD64", "X86", "RISCV").Install(without)
	with := newExternalDagqlServerForTest(t, Query{})
	aliasArchs.Install(with)
	require.Equal(t, without.Schema().Types["AliasArch"], with.Schema().Types["AliasArch"])
	require.NotEqual(t, without.SchemaDigest(), with.SchemaDigest())
}

// Using an enum value alias adds an event naming it and its canonical name
// to the current span.
func TestEnumValueAliasesDeprecationEvent(t *testing.T) {
	ctx, srv, _ := newEnumAliasTestServer(t)
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("enum-aliases-test").Start(ctx, "query")
	_, err := srv.Query(ctx, `query($archs: [AliasArch!]) {
		build(arch: ARM_64, archs: $archs, platform: {arch: AMD64}) { archs }
	}`, map[string]any{"archs": []any{"ARM64", "AMD_64"}})
	require.NoError(t, err)
	span.End()

	var got []map[string]string
	for _, s := range sr.Ended() {
		if s.Name() != "query" {
			continue
		}
		for _, ev := range s.Events() {
			if ev.Name != telemetryattrs.DagAliasEvent {
				continue
			}
			attrs := map[string]string{}
			for _, kv := range ev.Attributes {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			got = append(got, attrs)
		}
	}
	require.Equal(t, []map[string]string{
		{
			telemetryattrs.DagAliasKindAttr:      "enumValue",
			telemetryattrs.DagAliasParentAttr:    "AliasArch",
			telemetryattrs.DagAliasNameAttr:      "ARM_64",
			telemetryattrs.DagAliasCanonicalAttr: "ARM64",
		},
		{
			telemetryattrs.DagAliasKindAttr:      "enumValue",
			telemetryattrs.DagAliasParentAttr:    "AliasArch",
			telemetryattrs.DagAliasNameAttr:      "AMD_64",
			telemetryattrs.DagAliasCanonicalAttr: "AMD64",
		},
	}, got)
}
