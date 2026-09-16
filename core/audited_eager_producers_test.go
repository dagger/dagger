package core

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestAuditedEagerProducersEvaluate(t *testing.T) {
	ctx, store, cache, srv, server := executionFixture(t)
	packaged, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	server.builtin = packaged
	config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{"Env":["SAVED=true"],"WorkingDir":"/saved"}}`)
	configDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(config), Size: int64(len(config))}
	require.NoError(t, content.WriteBlob(ctx, packaged, "config", bytes.NewReader(config), configDesc))
	manifest, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configDesc, Layers: []ocispec.Descriptor{}})
	require.NoError(t, err)
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
	require.NoError(t, content.WriteBlob(ctx, packaged, "manifest", bytes.NewReader(manifest), desc))
	requested := Platform{OS: "linux", Architecture: "arm64"}
	eager, err := BuiltInContainer(ctx, requested, desc.Digest.String())
	require.NoError(t, err)
	defer eager.OnRelease(ctx)
	require.Nil(t, eager.Lazy)
	producer := eager.completedRecipe.(*ContainerBuiltinLazy)
	require.Equal(t, requested, producer.Platform)
	require.Equal(t, "amd64", eager.Platform.Architecture)
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_builtinContainer", Type: dagql.NewResultCallType(eager.Type())}
	raw, err := producer.EncodePersisted(ctx, dagql.NewPersistEncodeContext(cache, 0, call))
	require.NoError(t, err)
	decoded, err := decodeContainerBuiltinLazy(raw)
	require.NoError(t, err)
	private := NewContainer(requested)
	private.Lazy = decoded
	require.NoError(t, decoded.Evaluate(ctx, private))
	defer private.OnRelease(ctx)
	require.Nil(t, private.Lazy)
	require.Same(t, decoded, private.completedRecipe)
	require.Equal(t, eager.Platform, private.Platform)
	require.Equal(t, eager.Config, private.Config)
	eagerFS, _ := eager.FS.Peek()
	privateFS, _ := private.FS.Peek()
	eagerPath, _, err := producedDirectoryOutput(eagerFS)
	require.NoError(t, err)
	privatePath, _, err := producedDirectoryOutput(privateFS)
	require.NoError(t, err)
	require.Equal(t, eagerPath, privatePath)
	require.NoError(t, packaged.Delete(ctx, desc.Digest))
	missing, err := decodeContainerBuiltinLazy(raw)
	require.NoError(t, err)
	require.ErrorContains(t, missing.Evaluate(ctx, NewContainer(requested)), "lookup builtin image manifest")
	t.Run("empty patch without producer", func(t *testing.T) {
		before, _ := store.Build(t, nil, "data", "before\n")
		after, err := store.Manager.GetBySnapshotID(ctx, before.SnapshotID())
		require.NoError(t, err)
		change, err := NewChangeset(ctx,
			producerDirectoryResult(t, ctx, cache, srv, "patchBefore", "/", before),
			producerDirectoryResult(t, ctx, cache, srv, "patchAfter", "/", after))
		require.NoError(t, err)
		patch, err := change.AsPatch(ctx)
		require.NoError(t, err)
		defer patch.OnRelease(ctx)
		body, _ := producedFileContents(t, ctx, patch)
		require.Empty(t, body)
		require.Nil(t, patch.Lazy)
		require.Nil(t, patch.completedRecipe)
		encoded, err := patch.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
		require.NoError(t, err)
		var saved persistedFilePayload
		require.NoError(t, json.Unmarshal(encoded.JSON, &saved))
		require.Empty(t, saved.LazyKind)
		require.Empty(t, saved.LazyJSON)
	})
	t.Run("saved schema bytes", func(t *testing.T) {
		data := bytes.Repeat([]byte(`{"saved":"schema"}`), 25000)
		saved := &FileBlobLazy{LazyState: NewLazyState(), Filename: "schema.json", Contents: data, Permissions: 0644}
		raw, err := saved.EncodePersisted(ctx, nil)
		require.NoError(t, err)
		decoded, err := decodePersistedFileLazy(ctx, nil, persistedFileLazyKindBlob, raw)
		require.NoError(t, err)
		server.srv = nil
		output := freshProducerFile()
		require.NoError(t, decoded.Evaluate(ctx, output))
		defer output.OnRelease(ctx)
		body, info := producedFileContents(t, ctx, output)
		require.Equal(t, data, body)
		require.EqualValues(t, 0644, info.Mode().Perm())
		path, _ := output.File.Peek()
		require.Equal(t, "/schema.json", path)
	})
}
