package dangv2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestReadCallFrames(t *testing.T) {
	frames := func(kinds string, payloads ...string) *bytes.Buffer {
		var buf bytes.Buffer
		for i, kind := range []byte(kinds) {
			require.NoError(t, writeFrame(&buf, kind, []byte(payloads[i])))
		}
		return &buf
	}
	var stdout, stderr strings.Builder

	result, failed, err := readCallFrames(frames("OEOR", "out1 ", "err", "out2", `{"a":1}`), &stdout, &stderr)
	require.NoError(t, err)
	require.Empty(t, failed)
	require.JSONEq(t, `{"a":1}`, string(result))
	require.Equal(t, "out1 out2", stdout.String())
	require.Equal(t, "err", stderr.String())

	_, failed, err = readCallFrames(frames("EX", "Traceback", "boom"), &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, "boom", failed)

	truncated := frames("R", `{"a":1}`)
	truncated.Truncate(truncated.Len() - 1)
	_, _, err = readCallFrames(truncated, &stdout, &stderr)
	require.ErrorContains(t, err, "exited during the call")

	_, _, err = readCallFrames(frames("Z", ""), &stdout, &stderr)
	require.ErrorContains(t, err, "unknown kind")

	var oversized bytes.Buffer
	oversized.Write([]byte{'R', 0xff, 0xff, 0xff, 0xff})
	_, _, err = readCallFrames(&oversized, &stdout, &stderr)
	require.ErrorContains(t, err, "over the limit")
}

func TestTailBuffer(t *testing.T) {
	var b tailBuffer
	_, err := b.Write(bytes.Repeat([]byte("a"), engineutil.MaxExecErrorOutputBytes))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("a", engineutil.MaxExecErrorOutputBytes), b.String())
	_, err = b.Write([]byte("end"))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf(engineutil.TruncationMessage, 3)+strings.Repeat("a", engineutil.MaxExecErrorOutputBytes-3)+"end", b.String())
}

// The request carries what the call member's stdin carries: the receiver and
// the arguments as JSON text, which the SDK decodes with json.loads.
func TestCallFramePayload(t *testing.T) {
	decode := func(fnCall *core.FunctionCall) callFrame {
		t.Helper()
		payload, err := callFramePayload(fnCall, "call-client")
		require.NoError(t, err)
		var f callFrame
		require.NoError(t, json.Unmarshal(payload, &f), string(payload))
		return f
	}

	constructor := decode(&core.FunctionCall{ParentName: "Probe"})
	require.Equal(t, "Probe", constructor.Request.ReceiverType)
	require.Empty(t, constructor.Request.ReceiverValue, "no receiver")
	require.Empty(t, constructor.Request.FnName)
	require.JSONEq(t, `{}`, constructor.Request.FnArgs)

	fnCall := &core.FunctionCall{
		Name:       "greet",
		ParentName: "Probe",
		Parent:     core.JSON(`{"log":"x"}`),
		InputArgs:  []*core.FunctionCallArgValue{{Name: "name", Value: core.JSON(`"world"`)}},
	}
	fnCall.SetProcessSpanContext(trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
	}))
	call := decode(fnCall)
	require.Equal(t, "greet", call.Request.FnName)
	require.JSONEq(t, `{"log":"x"}`, call.Request.ReceiverValue)
	require.JSONEq(t, `{"name":"world"}`, call.Request.FnArgs)
	require.Equal(t, "call-client", call.ClientID)
	require.Equal(t, "00-01000000000000000000000000000000-0200000000000000-01", call.Traceparent)
}

func TestModuleServerTable(t *testing.T) {
	key := moduleServerKey{sessionID: t.Name()}
	srv, start := acquireModuleServer(key)
	require.True(t, start)
	other, _ := acquireModuleServer(key)
	require.Nil(t, other, "a starting server sends the call to the call member")

	srv.release()
	again, start := acquireModuleServer(key)
	require.Same(t, srv, again)
	require.False(t, start)
	other, _ = acquireModuleServer(key)
	require.Nil(t, other, "a busy server sends the call to the call member")

	forgetModuleServer(key, srv)
	fresh, start := acquireModuleServer(key)
	require.NotSame(t, srv, fresh)
	require.True(t, start)
	forgetModuleServer(key, fresh)
}
