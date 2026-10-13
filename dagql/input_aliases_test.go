package dagql_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// aliasMessage is an input object whose callID field has the hidden alias
// callId, and whose toolURL field has the alias toolUrl in view v1 only.
type aliasMessage struct {
	CallID  string `name:"callID"`
	ToolURL string `name:"toolURL" default:""`
}

func (aliasMessage) TypeName() string {
	return "AliasMessage"
}

func (aliasMessage) InputFieldAliases() map[string]dagql.Aliases {
	return map[string]dagql.Aliases{
		"callID":  aliasesOf("callId"),
		"toolURL": {Names: []string{"toolUrl"}, View: dagql.ExactView("v1")},
	}
}

type aliasReply struct {
	Text string `field:"true"`
}

func (*aliasReply) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasReply", NonNull: true}
}

// The struct-tag field text has the hidden alias txt.
func (*aliasReply) FieldAliases() map[string]dagql.Aliases {
	return map[string]dagql.Aliases{"text": aliasesOf("txt")}
}

// A field declared with a struct tag gets the aliases its type declares.
func TestStructFieldAliases(t *testing.T) {
	ctx, srv, _ := newInputAliasTestServer(t)
	data, err := srv.Query(ctx, `{ send(message: {callID: "a"}) { txt text } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"send": map[string]any{"txt": "a@", "text": "a@"}}, roundTrip(t, data))
	require.Nil(t, srv.Schema().Types["AliasReply"].Fields.ForName("txt"))
}

// newInputAliasTestServer serves Query.send(message: AliasMessage!,
// messages: [AliasMessage!]): AliasReply, counting the calls.
func newInputAliasTestServer(t *testing.T) (context.Context, *dagql.Server, *atomic.Int32) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:              "dagql-input-alias-client",
		SessionID:             "dagql-input-alias-session",
		UseRecipeIDsByDefault: true,
	})
	ctx = dagql.ContextWithCache(ctx, newCache(t))

	srv := newExternalDagqlServerForTest(t, Query{})
	introspection.Install[Query](srv)
	dagql.MustInputSpec(aliasMessage{}).Install(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*aliasReply]{}))
	var calls atomic.Int32
	dagql.Fields[Query]{
		dagql.Func("send", func(ctx context.Context, self Query, args struct {
			Message  dagql.InputObject[aliasMessage]
			Messages dagql.Optional[dagql.ArrayInput[dagql.InputObject[aliasMessage]]]
		}) (*aliasReply, error) {
			calls.Add(1)
			text := args.Message.Value.CallID + "@" + args.Message.Value.ToolURL
			if args.Messages.Valid {
				for _, msg := range args.Messages.Value {
					text += "," + msg.Value.CallID
				}
			}
			return &aliasReply{Text: text}, nil
		}),
	}.Install(srv)
	dagql.Fields[*aliasReply]{}.Install(srv)
	return ctx, srv, &calls
}

// An input object field alias is taken as its field, inline, in a list or in
// a variable, and makes the canonical call.
func TestInputFieldAliasesQuery(t *testing.T) {
	ctx, srv, calls := newInputAliasTestServer(t)
	id := func(query string, vars map[string]any) string {
		t.Helper()
		data, err := srv.Query(ctx, query, vars)
		require.NoError(t, err)
		res := roundTrip(t, data)["send"].(map[string]any)
		require.Equal(t, "a@,b", res["text"])
		return res["id"].(string)
	}
	canonical := id(`{ send(message: {callID: "a"}, messages: [{callID: "b"}]) { id text } }`, nil)
	require.Equal(t, canonical, id(`{ send(message: {callId: "a"}, messages: [{callId: "b"}]) { id text } }`, nil))
	require.Equal(t, canonical, id(`query($m: AliasMessage!, $ms: [AliasMessage!]) {
		send(message: $m, messages: $ms) { id text }
	}`, map[string]any{"m": map[string]any{"callId": "a"}, "ms": []any{map[string]any{"callId": "b"}}}))
	require.Equal(t, int32(1), calls.Load(), "aliased queries should hit the canonical call's cache entry")

	// Through gqlgen's handler too, which validates variables by itself.
	var res struct {
		Send struct{ Text string }
	}
	req(t, newTestClient(srv), `{ send(message: {callId: "x"}) { text } }`, &res)
	require.Equal(t, "x@", res.Send.Text)
	require.NoError(t, newTestClient(srv).Post(`query($m: AliasMessage!) { send(message: $m) { text } }`, &res,
		client.Var("m", map[string]any{"callId": "y"})))
	require.Equal(t, "y@", res.Send.Text)

	// Giving a field by both names is an error, and it stays required.
	_, err := srv.Query(ctx, `{ send(message: {callID: "a", callId: "b"}) { text } }`, nil)
	require.ErrorContains(t, err, `input field "callID" given twice, as "callID" and "callId"`)
	_, err = srv.Query(ctx, `{ send(message: {toolURL: "u"}) { text } }`, nil)
	require.ErrorContains(t, err, `missing required input field "callID"`)
}

// Input field aliases limited to some views are accepted only in those.
func TestInputFieldAliasesViews(t *testing.T) {
	ctx, srv, _ := newInputAliasTestServer(t)
	srv.View = "v0"
	_, err := srv.Query(ctx, `{ send(message: {callId: "a", toolUrl: "u"}) { text } }`, nil)
	require.ErrorContains(t, err, `toolUrl`)

	srv.View = "v1"
	data, err := srv.Query(ctx, `{ send(message: {callId: "a", toolUrl: "u"}) { text } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"send": map[string]any{"text": "a@u"}}, roundTrip(t, data))
}

// Nothing a client can read shows the input field aliases; the schema
// queries are validated against has them, deprecated.
func TestInputFieldAliasesHidden(t *testing.T) {
	ctx, srv, _ := newInputAliasTestServer(t)
	srv.View = "v1"

	def := srv.Schema().Types["AliasMessage"]
	require.Nil(t, def.Fields.ForName("callId"))
	require.True(t, def.Fields.ForName("callID").Type.NonNull)
	data, err := srv.Query(ctx, `{ __type(name: "AliasMessage") { inputFields(includeDeprecated: true) { name } } }`, nil)
	require.NoError(t, err)
	require.NotContains(t, string(mustJSON(t, data)), `"callId"`)

	aliased := srv.SchemaWithAliases().Types["AliasMessage"]
	var names []string
	for _, field := range aliased.Fields {
		names = append(names, field.Name)
	}
	require.Equal(t, []string{"callID", "callId", "toolURL", "toolUrl"}, names)
	require.Equal(t, "Use `callID` instead.", deprecationReason(aliased.Fields.ForName("callId").Directives))
	require.False(t, aliased.Fields.ForName("callID").Type.NonNull, "either name may give it")
	require.True(t, srv.Schema().Types["AliasMessage"].Fields.ForName("callID").Type.NonNull, "the schema itself didn't change")
}

// Using an input field alias adds an event naming it and its canonical name
// to the current span.
func TestInputFieldAliasesDeprecationEvent(t *testing.T) {
	ctx, srv, _ := newInputAliasTestServer(t)
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("input-aliases-test").Start(ctx, "query")
	_, err := srv.Query(ctx, `{ send(message: {callId: "a"}) { text } }`, nil)
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
	require.Equal(t, []map[string]string{{
		telemetryattrs.DagAliasKindAttr:      "inputField",
		telemetryattrs.DagAliasParentAttr:    "AliasMessage",
		telemetryattrs.DagAliasNameAttr:      "callId",
		telemetryattrs.DagAliasCanonicalAttr: "callID",
	}}, got)
}
