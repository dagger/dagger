package remotecache

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// The engine's connection reads a message the protocol compressed as its
// JSON, and writes a large one compressed, as a binary message, and a small
// one as text: each reads back on the other side as the JSON it was.
func TestConnectionReadsAndWritesCompressedMessages(t *testing.T) {
	t.Parallel()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		ws.SetReadLimit(protocol.MaxMessageBytes)
		accepted <- ws
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client, resp, err := websocket.Dial(t.Context(), "ws://"+strings.TrimPrefix(server.URL, "http://"), &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	require.NoError(t, err)
	client.SetReadLimit(protocol.MaxMessageBytes)
	conn := websocketConn{c: client, arrived: &arrivals{}}
	service := <-accepted
	t.Cleanup(func() {
		_ = client.CloseNow()
		_ = service.CloseNow()
	})

	large := protocol.Merge{Bundle: mergeTestBundle(400)}
	for name, c := range map[string]struct {
		typ        protocol.Type
		body       any
		compressed bool
	}{
		"a merge, compressed": {protocol.TypeMerge, large, true},
		"a probe, plain":      {protocol.TypeProbe, protocol.Probe{}, false},
	} {
		want, err := protocol.EncodeEnvelope(7, 0, c.typ, c.body)
		require.NoError(t, err, name)
		msg, err := protocol.EncodeMessage(7, 0, c.typ, c.body, protocol.MaxMessageBytes)
		require.NoError(t, err, name)
		require.Equal(t, c.compressed, msg.Compressed, name)

		typ := websocket.MessageText
		if msg.Compressed {
			typ = websocket.MessageBinary
		}
		require.NoError(t, service.Write(t.Context(), typ, msg.Data), name)
		got, err := conn.Read(t.Context())
		require.NoError(t, err, name)
		require.Equal(t, string(want), string(got), "%s: read by the engine", name)

		require.NoError(t, conn.Write(t.Context(), msg), name)
		gotTyp, data, err := service.Read(t.Context())
		require.NoError(t, err, name)
		require.Equal(t, typ, gotTyp, name)
		got, err = protocol.DecodeMessage(data, gotTyp == websocket.MessageBinary, protocol.MaxMessageBytes)
		require.NoError(t, err, name)
		require.Equal(t, string(want), string(got), "%s: written by the engine", name)
	}
}

// mergeTestBundle is a bundle of records records, each with an object's JSON
// and its call, over the record before it.
func mergeTestBundle(records int) dagql.ValueBundle {
	object := json.RawMessage(`{"rootfs":{"id":"` + strings.Repeat("a", 64) + `"},"config":{"workdir":"/src","entrypoint":["/bin/sh","-c"]},"platform":"linux/arm64"}`)
	bundle := dagql.ValueBundle{Version: dagql.ValueBundleVersion, Roots: []dagql.TransferredRoot{{Ordinal: dagql.TransferOrdinal(records)}}}
	for i := range records {
		v := dagql.TransferredValue{
			Ordinal:      dagql.TransferOrdinal(i + 1),
			SenderNumber: uint64(1000 + i),
			Record: dagql.PersistedRecord{
				ResultID: uint64(1000 + i),
				Envelope: dagql.PersistedResultEnvelope{Version: 1, Kind: "object", TypeName: "Container", ObjectCodec: "core", ResultID: uint64(1000 + i), ObjectJSON: object},
				Call:     &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "withExec", EffectIDs: []string{fmt.Sprintf("effect-%d", i)}},
			},
		}
		if i > 0 {
			v.DependencyIDs = []uint64{uint64(i)}
		}
		bundle.Values = append(bundle.Values, v)
	}
	return bundle
}
