package schema

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestContainerTransferPendingChild(t *testing.T) {
	// A captured filesystem descriptor carries no locally usable snapshot.
	payload := json.RawMessage(`{"operationState":"none","metadata":{"consumed":true,"value":{"platform":"linux/amd64","config":{"WorkingDir":"/captured"},"defaultTerminalCmd":{}}},"parts":{"fs":{"kind":"pending","valueKind":"directory","role":"fs","path":"/"},"execMeta":{"kind":"absent"}}}`)
	ctr := &core.Container{}
	family, ok := dagql.PersistedObjectFamilyFor(ctr)
	require.True(t, ok)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "capturedContainer", Type: dagql.NewResultCallType(ctr.Type())}
	bundle := dagql.ValueBundle{Version: 2, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{{Ordinal: 1, Record: dagql.PersistedRecord{
		ResultID: 1, Call: frame, Envelope: dagql.PersistedResultEnvelope{Version: 5, Kind: "object_self", TypeName: "Container", ObjectCodec: family.Name, ResultID: 1, ObjectJSON: payload},
	}}}}
	b := &persistedSchemaTestEnv{dbPath: filepath.Join(t.TempDir(), "b.db")}
	ctx, cache, srv := b.open(t)
	srv.View = "v0.21.0"
	imported, err := cache.ImportValues(ctx, bundle)
	require.NoError(t, err)
	require.Len(t, imported, 1)
	id := call.NewEngineResultID(imported[0].ResultID, call.NewType(ctr.Type()))
	handle, err := id.Encode()
	require.NoError(t, err)
	loaded, err := cache.LoadResultByResultID(ctx, persistedSchemaTestSession, srv, imported[0].ResultID)
	require.NoError(t, err)
	require.True(t, dagql.HasPendingLazyEvaluation(loaded))
	require.True(t, dagql.HasPendingLazyComputation(loaded))
	foreign, ok := dagql.UnwrapAs[*core.Container](loaded)
	require.True(t, ok)
	require.ErrorIs(t, foreign.Evaluate(ctx), dagql.ErrUnavailablePart)
	_, err = foreign.ImageConfig(ctx)
	require.NoError(t, err, "direct metadata remains usable")
	vars := map[string]any{"id": handle}
	// Metadata evaluation runs the delegation sweep; pending fs must be skipped.
	data, err := srv.Query(ctx, `query($id:ContainerID!){loadContainerFromID(id:$id){withEnvVariable(name:"X",value:"Y"){envVariable(name:"X") workdir}}}`, vars)
	require.NoError(t, err)
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	require.JSONEq(t, `{"loadContainerFromID":{"withEnvVariable":{"envVariable":"Y","workdir":"/captured"}}}`, string(raw))
	_, err = srv.Query(ctx, `query($id:ContainerID!){loadContainerFromID(id:$id){withEnvVariable(name:"X",value:"Y"){rootfs{entries}}}}`, vars)
	require.ErrorContains(t, err, dagql.ErrUnavailablePart.Error())
}
