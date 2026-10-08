package idtui

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// The span detail and the attribute filter read attributes back out of what
// dagui kept at ingest: the verbatim ExtraAttributes for keys it does not
// interpret (the engine's dagger.git.* measurements), and the typed fields
// for the ones it does. Driven through real OTLP export so ProcessAttribute
// is what decides which is which.
func TestSpanDetailAndListShowAttributes(t *testing.T) {
	const (
		fallbackAttr = "dagger.git.checkout.cow.fallback"
		reasonAttr   = "dagger.git.native.fallback_reason"
		bytesAttr    = "dagger.git.checkout.cow.layer_bytes"
		pathAttr     = "dagger.git.tree.path"
	)
	longPath := strings.Repeat("a/", 500) // 1000 bytes, 1002 JSON-quoted

	db := dagui.NewDB()
	require.NoError(t, db.ExportSpans(context.Background(), telemetry.SpansFromPB(cannedTrace(liveTraceIDByte,
		cannedSpan{id: 1, name: "root", start: 100, end: 200},
		cannedSpan{
			id: 2, parent: 1, name: "checkout", start: 110, end: 120,
			attrs: []*commonpb.KeyValue{
				cannedBoolAttr(fallbackAttr, true),
				cannedStringAttr(reasonAttr, "no native support for sha256"),
				{Key: bytesAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 12345}}},
				cannedStringAttr(pathAttr, longPath),
				cannedStringAttr("dagger.git.ref", "<main> & co"),
			},
		},
		cannedSpan{
			id: 3, parent: 1, name: "turn", start: 130, end: 140,
			attrs: cannedMessageAttrs("assistant"),
		},
	).GetResourceSpans())))

	checkout := prettyTestSpanID(2)
	detail, ok := RenderSpanDetail(db, checkout)
	require.True(t, ok)
	for _, want := range []string{
		"attributes (as recorded):\n",
		"  " + bytesAttr + " = 12345\n",
		"  " + fallbackAttr + " = true\n",
		"  " + reasonAttr + " = \"no native support for sha256\"\n",
		"  " + pathAttr + " = \"" + longPath[:InspectAttrMaxLen-1] + "… (1002 bytes)\n",
		// Not HTML-escaped (\u003c...).
		"  dagger.git.ref = \"<main> & co\"\n",
	} {
		require.Contains(t, detail, want)
	}
	// Sorted by key.
	require.Less(t, strings.Index(detail, fallbackAttr), strings.Index(detail, bytesAttr))

	full, ok := RenderSpanDetailWith(db, checkout, 0)
	require.True(t, ok)
	require.Contains(t, full, "  "+pathAttr+" = \""+longPath+"\"\n")

	// A parsed attribute is reported under its OTel key, apart from the
	// verbatim ones.
	turn, ok := RenderSpanDetail(db, prettyTestSpanID(3))
	require.True(t, ok)
	require.Contains(t, turn, "attributes (parsed by the UI; rebuilt from its fields):\n  "+
		telemetry.LLMRoleAttr+" = \"assistant\"\n")
	require.NotContains(t, turn, "attributes (as recorded)")

	root, ok := RenderSpanDetail(db, prettyTestSpanID(1))
	require.True(t, ok)
	require.Contains(t, root, "attributes: (none)\n")

	list := func(spec string) string {
		f := ParseSpanAttrFilter(spec)
		return RenderSpanListFiltered(db, "", &f, 0)
	}
	require.Equal(t, checkout.String()+"  ok     checkout  ["+fallbackAttr+"=true]\n", list(fallbackAttr))
	require.Equal(t, checkout.String()+"  ok     checkout  ["+reasonAttr+"=\"no native support for sha256\"]\n",
		list(reasonAttr+"=native support"))
	require.Contains(t, list(bytesAttr+"=123"), "checkout")
	require.Empty(t, list(reasonAttr+"=nope"))
	require.Empty(t, list("dagger.git.absent"))
	require.Equal(t, prettyTestSpanID(3).String()+"  ok     turn  ["+telemetry.LLMRoleAttr+"=\"assistant\"]\n",
		list(telemetry.LLMRoleAttr+"=assistant"))
	// The listing's values are capped too.
	require.Contains(t, list(pathAttr), "… (1002 bytes)]")
}
