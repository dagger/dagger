package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestValueTransferForeignFormsImport(t *testing.T) {
	for _, tc := range []struct{ family, native string }{
		{"File", `{"form":"snapshot","file":"/data","platform":"linux/amd64"}`},
		{"Directory", `{"form":"snapshot","dir":"/data","platform":"linux/amd64"}`},
		{"Container", `{"metadata":{"consumed":true,"value":{"platform":"linux/amd64","config":{},"defaultTerminalCmd":{}}},"parts":{"fs":{"kind":"directory","role":"fs","path":"/"},"execMeta":{"kind":"absent"}}}`},
		{"HTTPState", `{"form":"native","url":"https://example.test/body","etag":"old","contentDigest":"sha256:old"}`},
		{"CacheVolume", `{"form":"native","key":"build"}`},
		{"ClientFilesyncMirror", `{"form":"native","stableClientID":"host","drive":"/"}`},
		{"RemoteGitMirror", `{"form":"native","remoteURL":"https://example.test/repo"}`},
		{"ModuleSource", `{"kind":"LOCAL_SOURCE","moduleName":"probe","local":{"ContextDirectoryPath":"/producer"}}`},
	} {
		t.Run(tc.family, func(t *testing.T) {
			env := newPersistedFamiliesTestEnv(t, "consumer")
			ctx, cache, _ := env.open(t)
			normalized, err := foreignFamilyCodec(tc.family).NormalizeForeign(dagql.PersistedPayloadVisit{Payload: json.RawMessage(tc.native)})
			require.NoError(t, err)
			for _, inline := range []bool{false, true} {
				makeBundle := func(payload json.RawMessage) dagql.ValueBundle {
					typ := &dagql.ResultCallType{NamedType: tc.family, NonNull: true}
					envelope := dagql.PersistedResultEnvelope{Version: 5, ResultID: 1, Kind: "object_self", TypeName: tc.family, ObjectCodec: "core." + tc.family, ObjectJSON: payload}
					if inline {
						envelope.ResultID = 0
						envelope = dagql.PersistedResultEnvelope{Version: 5, ResultID: 1, Kind: "list", Items: []dagql.PersistedResultEnvelope{envelope}}
						typ = &dagql.ResultCallType{Elem: typ, NonNull: true}
					}
					return dagql.ValueBundle{Version: 2, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{{Ordinal: 1, Record: dagql.PersistedRecord{ResultID: 1, Envelope: envelope, Call: &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "foreignForm", Type: typ}}}}}
				}
				before := len(cache.DebugEGraphSnapshot().Results)
				_, err := cache.ImportValues(ctx, makeBundle(json.RawMessage(tc.native)))
				require.Error(t, err)
				require.Len(t, cache.DebugEGraphSnapshot().Results, before, "invalid foreign form cannot publish")
				values, err := cache.ImportValues(ctx, makeBundle(normalized.JSON))
				require.NoError(t, err)
				require.Len(t, values, 1)
			}
		})
	}
}
