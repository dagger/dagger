package codexapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnReadsRequestsNotificationsAndParseErrors(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"x","version":"1"}}}`,
		``,
		`{"method":"initialized"}`,
		`not json`,
		`{"id":"abc","method":"thread/start"}`,
	}, "\n")
	conn := NewConn(strings.NewReader(input), io.Discard)

	msg, err := conn.Read()
	require.NoError(t, err)
	require.True(t, msg.IsRequest())
	require.Equal(t, "initialize", msg.Method)
	require.JSONEq(t, `1`, string(msg.ID))

	msg, err = conn.Read()
	require.NoError(t, err)
	require.True(t, msg.IsNotification())
	require.False(t, msg.IsRequest())
	require.Equal(t, "initialized", msg.Method)

	_, err = conn.Read()
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int64(CodeParseError), rpcErr.Code)

	// The connection survives a bad line.
	msg, err = conn.Read()
	require.NoError(t, err)
	require.True(t, msg.IsRequest())
	require.JSONEq(t, `"abc"`, string(msg.ID))

	_, err = conn.Read()
	require.True(t, errors.Is(err, io.EOF))
}

func TestConnWritesCodexDialect(t *testing.T) {
	var out bytes.Buffer
	conn := NewConn(strings.NewReader(""), &out)

	require.NoError(t, conn.Reply(json.RawMessage(`7`), map[string]any{"ok": true}))
	require.NoError(t, conn.Reply(json.RawMessage(`"s"`), nil))
	require.NoError(t, conn.ReplyError(nil, Errorf(CodeParseError, "bad")))
	require.NoError(t, conn.Notify("thread/started", map[string]any{"thread": "t"}))
	require.NoError(t, conn.Notify("initialized", nil))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 5)
	// No "jsonrpc" member anywhere: that is the Codex dialect.
	require.NotContains(t, out.String(), "jsonrpc")
	require.JSONEq(t, `{"id":7,"result":{"ok":true}}`, lines[0])
	require.JSONEq(t, `{"id":"s","result":{}}`, lines[1])
	require.JSONEq(t, `{"id":null,"error":{"code":-32700,"message":"bad"}}`, lines[2])
	require.JSONEq(t, `{"method":"thread/started","params":{"thread":"t"}}`, lines[3])
	require.JSONEq(t, `{"method":"initialized","params":{}}`, lines[4])
}

func TestThreadStatusJSON(t *testing.T) {
	idle, err := json.Marshal(ThreadStatus{Type: ThreadStatusIdle})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"idle"}`, string(idle))

	active, err := json.Marshal(ThreadStatus{Type: ThreadStatusActive})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"active","activeFlags":[]}`, string(active))

	var back ThreadStatus
	require.NoError(t, json.Unmarshal(active, &back))
	require.Equal(t, ThreadStatusActive, back.Type)
}
