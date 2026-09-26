package archive

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestVerifyClosure(t *testing.T) {
	typ := &ast.Type{NamedType: "LLM", NonNull: true}
	root := call.New().Append(typ, "llm")
	child := root.Append(typ, "withPrompt")
	calls := map[string]*callpbv1.Call{
		root.Digest().String():  root.Call(),
		child.Digest().String(): child.Call(),
	}
	load := func(d string) (*callpbv1.Call, error) {
		if c := calls[d]; c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("missing call %s", d)
	}
	closure, err := VerifyClosure([]string{child.Digest().String()}, load)
	require.NoError(t, err)
	require.Len(t, closure, 2)

	delete(calls, root.Digest().String())
	_, err = VerifyClosure([]string{child.Digest().String()}, load)
	require.Error(t, err, "a missing dependency breaks the closure")

	cyclic := child.Call()
	cyclic.ReceiverDigest = cyclic.Digest
	calls = map[string]*callpbv1.Call{cyclic.Digest: cyclic}
	_, err = VerifyClosure([]string{cyclic.Digest}, load)
	require.ErrorContains(t, err, "cyclic")
}

func TestBootstrapTruncationNeverAppliesPartialSignals(t *testing.T) {
	data, _, err := BuildBootstrap(BootstrapHeader{TraceID: testTraceA, SealAt: time.Now().UTC().Format(time.RFC3339Nano)}, []BootstrapSignal{{}})
	require.NoError(t, err)
	client, closeServer := bootstrapTestClient(t, data[:len(data)-1])
	defer closeServer()
	callbacks := 0
	_, err = client.Bootstrap(context.Background(), testTraceA, func(BootstrapHeader, BootstrapBatch) error { callbacks++; return nil })
	require.ErrorIs(t, err, ErrTransient)
	require.Zero(t, callbacks)
	require.False(t, IsCleanMiss(err))
}
