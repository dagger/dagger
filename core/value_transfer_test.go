package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestValueTransferForeignForms(t *testing.T) {
	for _, tc := range []struct{ family, native string }{
		{"File", `{"form":"snapshot","file":"/data","platform":"linux/amd64"}`},
		{"Directory", `{"form":"snapshot","dir":"/src","platform":"linux/amd64"}`},
		{"HTTPState", `{"form":"native","url":"https://example.test/file","etag":"old","lastModified":"yesterday","contentDigest":"sha256:old"}`},
		{"CacheVolume", `{"form":"native","key":"build","sourceResultID":9007199254740993,"selector":"/"}`},
		{"ClientFilesyncMirror", `{"form":"native","stableClientID":"alice","drive":"/"}`},
		{"RemoteGitMirror", `{"form":"native","remoteURL":"https://example.test/repo"}`},
		{"ModuleSource", `{"kind":"LOCAL_SOURCE","moduleName":"test","local":{"ContextDirectoryPath":"/alice/project"}}`},
		{"Container", `{"metadata":{"consumed":true,"value":{"platform":"linux/amd64","config":{},"defaultTerminalCmd":{}}},"parts":{"fs":{"kind":"directory","role":"fs","path":"/"},"execMeta":{"kind":"absent"}}}`},
	} {
		t.Run(tc.family, func(t *testing.T) {
			codec := foreignFamilyCodec(tc.family)
			v := dagql.PersistedPayloadVisit{Payload: json.RawMessage(tc.native)}
			require.Error(t, codec.ValidateForeign(v))
			foreign, err := codec.NormalizeForeign(v)
			require.NoError(t, err)
			require.Equal(t, tc.native, string(v.Payload))
			v.Payload = foreign.JSON
			require.NoError(t, codec.ValidateForeign(v))
			again, err := codec.NormalizeForeign(v)
			require.NoError(t, err)
			require.JSONEq(t, string(foreign.JSON), string(again.JSON))
			v.SnapshotLinks = []dagql.PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "alice-storage"}}
			require.ErrorContains(t, codec.ValidateForeign(v), "local storage")
		})
	}
	for _, tc := range []struct{ family, invalid string }{
		{"CacheVolume", `{"key":"build"}`},
		{"CacheVolume", `{"form":"foreign_uninitialized","key":"build","snapshotID":"old"}`},
		{"HTTPState", `{"form":"foreign_uninitialized","url":"url","etag":"old"}`},
		{"HTTPState", `{"form":"foreign_uninitialized","url":"url","contentDigest":"old"}`},
		{"ClientFilesyncMirror", `{"stableClientID":"alice"}`},
		{"RemoteGitMirror", `{"remoteURL":"url"}`},
		{"File", `{"form":"transfer_pending","platform":"linux/amd64","producerState":"pending"}`},
		{"File", `{"form":"transfer_pending","platform":"linux/amd64","producerState":"none","lazyKind":"file.blob","lazyJSON":{}}`},
		{"Directory", `{"form":"transfer_pending","platform":"linux/amd64","producerState":"completed","lazyKind":"unknown","lazyJSON":{}}`},
	} {
		require.Error(t, foreignFamilyCodec(tc.family).ValidateForeign(dagql.PersistedPayloadVisit{Payload: json.RawMessage(tc.invalid)}), "%s: %s", tc.family, tc.invalid)
	}
}

func TestValueTransferParts(t *testing.T) {
	raw := json.RawMessage(`{"form":"snapshot","file":"/parent/nested/one","platform":"linux/amd64"}`)
	codec := foreignFamilyCodec("File")
	v := dagql.PersistedPayloadVisit{Payload: raw, SnapshotLinks: []dagql.PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "whole-parent"}}}
	outputs, err := codec.MapSnapshotParts(v)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "whole-parent", outputs[0].SnapshotID)
	require.Equal(t, "/parent/nested/one", outputs[0].Value.Path)
	v.SnapshotLinks = nil
	_, err = codec.MapSnapshotParts(v)
	require.ErrorContains(t, err, "missing snapshot role")
	normalized, err := codec.NormalizeForeign(v)
	require.NoError(t, err)
	v.Payload = normalized.JSON
	outputs, err = codec.MapSnapshotParts(v)
	require.NoError(t, err)
	require.Equal(t, "pending", outputs[0].State)
	require.Empty(t, outputs[0].SnapshotID)
	value, err := (&File{}).DecodePersistedObject(t.Context(), dagql.NewPersistDecodeContext(nil, 0, nil), v.Payload)
	require.NoError(t, err)
	file := value.(*File)
	path, known := file.File.Peek()
	require.True(t, known)
	require.Equal(t, "/parent/nested/one", path)
	knownPath, err := file.PathOrEval(t.Context(), dagql.ObjectResult[*File]{})
	require.NoError(t, err)
	require.Equal(t, path, knownPath)
	require.ErrorIs(t, file.LazyEvalFunc()(t.Context()), dagql.ErrUnavailablePart)
	require.Nil(t, file.Lazy)
	encoded, err := file.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	require.JSONEq(t, string(normalized.JSON), string(encoded.JSON))
}
