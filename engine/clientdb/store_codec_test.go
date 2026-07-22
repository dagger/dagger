package clientdb

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreCodecRoundTrip(t *testing.T) {
	t.Run("span", func(t *testing.T) {
		want := Span{
			ID:                     42,
			TraceID:                "trace",
			SpanID:                 "span",
			TraceState:             "state",
			ParentSpanID:           sql.NullString{String: "parent", Valid: true},
			Flags:                  -3,
			Name:                   "name",
			Kind:                   "server",
			StartTime:              -100,
			EndTime:                sql.NullInt64{Int64: 200, Valid: true},
			Attributes:             nil,
			DroppedAttributesCount: 4,
			Events:                 []byte{},
			DroppedEventsCount:     5,
			Links:                  []byte("links"),
			DroppedLinksCount:      6,
			StatusCode:             7,
			StatusMessage:          "status",
			InstrumentationScope:   []byte("scope"),
			Resource:               []byte("resource"),
			ResourceSchemaUrl:      "resource-schema",
		}
		got, err := spanCodec.decode(spanCodec.encode(want))
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Nil(t, got.Attributes)
		require.NotNil(t, got.Events)
	})

	t.Run("span null payloads", func(t *testing.T) {
		want := Span{
			ParentSpanID: sql.NullString{String: "preserved while invalid"},
			EndTime:      sql.NullInt64{Int64: -99},
		}
		got, err := spanCodec.decode(spanCodec.encode(want))
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("log", func(t *testing.T) {
		want := Log{
			ID:                   99,
			TraceID:              sql.NullString{String: "trace", Valid: true},
			SpanID:               sql.NullString{String: "invalid-but-preserved"},
			Timestamp:            -123,
			SeverityNumber:       17,
			SeverityText:         "WARN",
			Body:                 nil,
			Attributes:           []byte{},
			InstrumentationScope: []byte("scope"),
			Resource:             []byte("resource"),
			ResourceSchemaUrl:    "schema",
		}
		got, err := logCodec.decode(logCodec.encode(want))
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Nil(t, got.Body)
		require.NotNil(t, got.Attributes)
	})

	t.Run("metric", func(t *testing.T) {
		for _, want := range []Metric{
			{ID: 1, Data: nil},
			{ID: 2, Data: []byte{}},
			{ID: 3, Data: []byte("metric")},
		} {
			got, err := metricCodec.decode(metricCodec.encode(want))
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
	})
}

func TestStoreCodecHugeRow(t *testing.T) {
	want := Metric{ID: 7, Data: bytes.Repeat([]byte("0123456789abcdef"), 1<<20)}
	encoded := metricCodec.encode(want)
	require.Greater(t, len(encoded), 16<<20)

	got, err := metricCodec.decode(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestStoreCodecRejectsMalformedRows(t *testing.T) {
	_, err := metricCodec.decode([]byte{0x80})
	require.ErrorContains(t, err, "truncated varint")

	valid := metricCodec.encode(Metric{ID: 1, Data: []byte("ok")})
	_, err = metricCodec.decode(append(valid, 0))
	require.ErrorContains(t, err, "trailing bytes")
}
