package dangshared

import (
	"bytes"
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/stretchr/testify/require"
)

func TestWithNestedClientServerRecordsCaller(t *testing.T) {
	t.Parallel()
	wcprof.EnsureRecorder()
	ctx, caller := wcprof.BeginOp(wcprof.ContextWithProfiling(scopedContext(t, "parent")),
		wcprof.OpKindCallExec, "native-call", wcprof.OpOpts{})
	_, err := WithNestedClientServer(ctx, &core.Query{Server: &nestedTransportTestServer{}},
		&engine.ClientMetadata{SessionID: "session", ClientID: "profile-child"},
		false, nil, dagql.ObjectResult[*core.Module]{},
		func(context.Context, graphql.Client) ([]byte, error) { return nil, nil })
	caller.End(wcprof.OutcomeOK)
	require.NoError(t, err)
	var dump bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&dump, false))
	header, events, err := wcprof.ReadDump(bytes.NewReader(dump.Bytes()))
	require.NoError(t, err)
	for _, event := range events {
		if event.LinkKind == "nested_client" && event.ParentID == caller.ID() {
			require.Equal(t, "profile-child", header.Strings[event.IdentID])
			return
		}
	}
	t.Fatal("nested client has no link to its caller in the profile")
}
