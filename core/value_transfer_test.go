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
		{"File", `{"form":"transfer_pending","platform":"linux/amd64","operationState":"pending"}`},
		{"File", `{"form":"transfer_pending","platform":"linux/amd64","operationState":"none","lazyKind":"file.blob","lazyJSON":{}}`},
		{"Directory", `{"form":"transfer_pending","platform":"linux/amd64","operationState":"evaluated","lazyKind":"unknown","lazyJSON":{}}`},
	} {
		require.Error(t, foreignFamilyCodec(tc.family).ValidateForeign(dagql.PersistedPayloadVisit{Payload: json.RawMessage(tc.invalid)}), "%s: %s", tc.family, tc.invalid)
	}
}

// A foreign payload its family validated stays valid once the family's
// visitor relocates its references, as a stored bundle relocates the records
// it copies to its ordinals after validating them. Every foreign family has a
// sample, and each reference a sample makes, through service bindings, lazy
// operations, a cache volume's source and module dependencies, is moved.
func TestValueTransferRelocatedForeignStaysValid(t *testing.T) {
	samples := map[string]struct {
		field, native string
		refs          int
	}{
		"File":                 {"", `{"form":"lazy","file":"/data","platform":"linux/amd64","services":[{"serviceResultID":7,"hostname":"db"}],"lazyKind":"file.withName","lazyJSON":{"parentResultID":8,"filename":"x"}}`, 2},
		"Directory":            {"", `{"form":"lazy","dir":"/","platform":"linux/amd64","services":[{"serviceResultID":7,"hostname":"db"}],"lazyKind":"directory.withFile","lazyJSON":{"parentResultID":8,"destPath":"/a.txt","sourceResultID":9}}`, 3},
		"Container":            {"withUser", `{"metadata":{"consumed":true,"value":{"platform":"linux/amd64","config":{},"defaultTerminalCmd":{},"services":[{"serviceResultID":7,"hostname":"db"}]}},"parts":{"fs":{"kind":"directory","role":"fs","path":"/","services":[{"serviceResultID":10,"hostname":"cache"}]},"execMeta":{"kind":"absent"}},"lazyJSON":{"parentResultID":8,"name":"root"}}`, 3},
		"CacheVolume":          {"", `{"form":"native","key":"build","sourceResultID":9,"selector":"/"}`, 1},
		"ModuleSource":         {"", `{"kind":"LOCAL_SOURCE","moduleName":"test","local":{"ContextDirectoryPath":"/alice/project"},"dependencyResultIDs":[11,12]}`, 2},
		"HTTPState":            {"", `{"form":"native","url":"https://example.test/file"}`, 0},
		"ClientFilesyncMirror": {"", `{"form":"native","stableClientID":"alice","drive":"/"}`, 0},
		"RemoteGitMirror":      {"", `{"form":"native","remoteURL":"https://example.test/repo"}`, 0},
	}
	for _, family := range dagql.PersistedObjectFamilies() {
		codec, ok := family.Transfer.(foreignFamilyCodec)
		if !ok {
			continue
		}
		sample, ok := samples[string(codec)]
		require.True(t, ok, "family %s has no sample", family.Name)
		v := dagql.PersistedPayloadVisit{Payload: json.RawMessage(sample.native)}
		if sample.field != "" {
			v.Call = &dagql.ResultCall{Field: sample.field}
		}
		foreign, err := codec.NormalizeForeign(v)
		require.NoError(t, err, family.Name)
		v.Payload = foreign.JSON
		require.NoError(t, codec.ValidateForeign(v), family.Name)
		moved := 0
		relocated, err := family.Visitor.VisitPersistedReferences(v, func(ref *dagql.PersistedRef) error {
			if ref.RecipeID == nil && ref.ResultID != 0 {
				ref.ResultID += 1000
				moved++
			}
			return nil
		})
		require.NoError(t, err, family.Name)
		require.Equal(t, sample.refs, moved, family.Name)
		if moved > 0 {
			require.NotEqual(t, string(v.Payload), string(relocated), family.Name)
		}
		v.Payload = relocated
		require.NoError(t, codec.ValidateForeign(v), "%s, relocated", family.Name)
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

func TestLazyOperationTransferState(t *testing.T) {
	for _, form := range []string{"lazy", "snapshot"} {
		t.Run(form, func(t *testing.T) {
			codec := foreignFamilyCodec("Directory")
			v := dagql.PersistedPayloadVisit{Payload: json.RawMessage(`{"form":"` + form + `","dir":"/","platform":"linux/amd64","lazyKind":"scratch","lazyJSON":{}}`)}
			out, err := codec.NormalizeForeign(v)
			require.NoError(t, err)
			var payload persistedDirectoryPayload
			require.NoError(t, json.Unmarshal(out.JSON, &payload))
			want := "pending"
			if form == "snapshot" {
				want = "evaluated"
			}
			require.Equal(t, want, payload.OperationState)
			require.Equal(t, transferPending, payload.Form)
			v.Payload = out.JSON
			route, err := codec.RouteParts(v, "snapshot")
			require.NoError(t, err)
			require.True(t, route.HasLazyOperation)
			probes, err := codec.DescribeParts(v)
			require.NoError(t, err)
			require.Len(t, probes, 1)
			require.False(t, probes[0].LocalComplete, "A's evaluated operation never marks B's part ready")
		})
	}
	for _, invalid := range []string{
		`{"form":"transfer_pending","operationState":"completed","lazyKind":"scratch","lazyJSON":{}}`,
		`{"form":"transfer_pending","operationState":"evaluated","lazyKind":"scratch","lazyJSON":{},"unknown":true}`,
		`{"form":"transfer_pending","operationState":"invented","lazyKind":"scratch","lazyJSON":{}}`,
	} {
		require.Error(t, foreignFamilyCodec("Directory").ValidateForeign(dagql.PersistedPayloadVisit{Payload: json.RawMessage(invalid)}))
	}
}
