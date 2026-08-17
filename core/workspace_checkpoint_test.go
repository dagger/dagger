package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCheckpointChunkBoundsAndDigest(t *testing.T) {
	t.Parallel()

	data := []byte("checkpoint chunk")
	chunk, err := NewWorkspaceCheckpointChunk(data, digest.FromBytes(data))
	require.NoError(t, err)
	require.Equal(t, data, chunk.Data())
	require.Equal(t, digest.FromBytes(data), chunk.Digest())

	_, err = NewWorkspaceCheckpointChunk(data, digest.FromString("different"))
	require.ErrorContains(t, err, "does not match content")

	_, err = NewWorkspaceCheckpointChunk(make([]byte, workspaceCheckpointMaxChunkBytes+1), "")
	require.ErrorContains(t, err, "maximum")
}

func TestAssembleWorkspaceCheckpointPayloads(t *testing.T) {
	t.Parallel()

	bundleParts := [][]byte{[]byte("bundle-"), []byte("bytes")}
	worktreeParts := [][]byte{[]byte("worktree")}
	all := append(append([][]byte{}, bundleParts...), worktreeParts...)
	chunks := make([]*WorkspaceCheckpointChunk, 0, len(all))
	for _, data := range all {
		chunk, err := NewWorkspaceCheckpointChunk(data, digest.FromBytes(data))
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
	payload := func(parts [][]byte) WorkspaceCheckpointPayload {
		var data []byte
		var descriptors []WorkspaceCheckpointChunkDescriptor
		for _, part := range parts {
			data = append(data, part...)
			descriptors = append(descriptors, WorkspaceCheckpointChunkDescriptor{
				Size:   len(part),
				Digest: digest.FromBytes(part).String(),
			})
		}
		return WorkspaceCheckpointPayload{
			Size:   int64(len(data)),
			Digest: digest.FromBytes(data).String(),
			Chunks: descriptors,
		}
	}
	manifest := &WorkspaceGitCheckpointManifest{
		Bundle:   payload(bundleParts),
		Worktree: payload(worktreeParts),
	}

	bundle, worktree, err := AssembleWorkspaceCheckpointPayloads(manifest, chunks)
	require.NoError(t, err)
	require.Equal(t, []byte("bundle-bytes"), bundle)
	require.Equal(t, []byte("worktree"), worktree)

	t.Run("truncated", func(t *testing.T) {
		_, _, err := AssembleWorkspaceCheckpointPayloads(manifest, chunks[:2])
		require.ErrorContains(t, err, "is missing")
	})
	t.Run("reordered", func(t *testing.T) {
		reordered := []*WorkspaceCheckpointChunk{chunks[1], chunks[0], chunks[2]}
		_, _, err := AssembleWorkspaceCheckpointPayloads(manifest, reordered)
		require.ErrorContains(t, err, "bundle chunk 0")
	})
	t.Run("unreferenced", func(t *testing.T) {
		extra, err := NewWorkspaceCheckpointChunk([]byte("extra"), "")
		require.NoError(t, err)
		_, _, err = AssembleWorkspaceCheckpointPayloads(manifest, append(chunks, extra))
		require.ErrorContains(t, err, "unreferenced")
	})
	t.Run("wrong aggregate digest", func(t *testing.T) {
		bad := *manifest
		bad.Bundle = manifest.Bundle
		bad.Bundle.Digest = digest.FromString("wrong").String()
		_, _, err := AssembleWorkspaceCheckpointPayloads(&bad, chunks)
		require.ErrorContains(t, err, "payload digest")
	})
}

func TestWorkspaceCheckpointExportTargetIsEphemeral(t *testing.T) {
	t.Parallel()

	ws := &Workspace{}
	ws.SetSource(NewWorkspaceSourceDirectory(dagql.ObjectResult[*Directory]{}))
	_, err := ws.ExportHostPath()
	require.ErrorContains(t, err, "synthetic workspace")

	ws.SetExportTarget("live-client", "/checkout")
	require.Equal(t, "live-client", ws.ExportClientID())
	hostPath, err := ws.ExportHostPath()
	require.NoError(t, err)
	require.Equal(t, "/checkout", hostPath)

	clone := ws.Clone()
	require.Equal(t, "live-client", clone.ExportClientID())
	hostPath, err = clone.ExportHostPath()
	require.NoError(t, err)
	require.Equal(t, "/checkout", hostPath)

	encoded, err := clone.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)
	require.NotContains(t, string(encoded.JSON), "live-client")
	require.NotContains(t, string(encoded.JSON), "/checkout")
}

func TestParseWorkspaceGitCheckpointManifestStrict(t *testing.T) {
	t.Parallel()

	manifest := WorkspaceGitCheckpointManifest{Version: WorkspaceCheckpointFormatVersion}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	parsed, err := ParseWorkspaceGitCheckpointManifest(string(raw))
	require.NoError(t, err)
	require.Equal(t, WorkspaceCheckpointFormatVersion, parsed.Version)

	_, err = ParseWorkspaceGitCheckpointManifest(strings.TrimSuffix(string(raw), "}") + `,"futureField":true}`)
	require.ErrorContains(t, err, "unknown field")

	manifest.Version++
	raw, err = json.Marshal(manifest)
	require.NoError(t, err)
	_, err = ParseWorkspaceGitCheckpointManifest(string(raw))
	require.ErrorContains(t, err, "unsupported workspace checkpoint format")
}
