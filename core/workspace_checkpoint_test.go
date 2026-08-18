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

// TestWorkspaceCheckpointExportTargetIsSessionState asserts how a checkpoint
// finds its save destination: by its own pure identity, through session state
// the capturing session retained, and never from anything the workspace value
// itself carries. Outside that session the same value has no destination at all.
func TestWorkspaceCheckpointExportTargetIsSessionState(t *testing.T) {
	// Not parallel: registers the process-global checkpoint-origin hooks.
	t.Cleanup(func() { SetWorkspaceCheckpointOriginHooks(nil, nil) })

	const checkpointID = "sha256:checkpoint"
	ws := &Workspace{}
	ws.SetSource(NewWorkspaceSourceDirectory(dagql.ObjectResult[*Directory]{}))
	ws.SetPortableCheckpoint()
	ws.SetWorkspaceEnv("dev")
	ws.SetCheckpointID(checkpointID)
	ws.SetGitOrigin("git@github.com:acme/repo.git")

	ctx := context.Background()

	// No session retained this checkpoint's origin: saving must fail rather
	// than fall back to a destination of its own.
	_, _, err := ws.ExportTarget(ctx)
	require.ErrorContains(t, err, "restored workspace checkpoint")

	var lookedUp string
	SetWorkspaceCheckpointOriginHooks(nil, func(_ context.Context, id string) (string, string, bool) {
		lookedUp = id
		if id != checkpointID {
			return "", "", false
		}
		return "live-client", "/checkout", true
	})

	// Derived workspaces keep the identity, so the agent's edits and commits
	// save to the same checkout the checkpoint was captured from.
	clone := ws.Clone()
	require.Equal(t, "dev", clone.WorkspaceEnv())
	require.Equal(t, checkpointID, clone.CheckpointID())
	clientID, hostPath, err := clone.ExportTarget(ctx)
	require.NoError(t, err)
	require.Equal(t, checkpointID, lookedUp)
	require.Equal(t, "live-client", clientID)
	require.Equal(t, "/checkout", hostPath)

	// The value itself remains host-independent: it carries only the pure
	// identity, so a persisted checkpoint cannot resurrect a destination.
	encoded, err := clone.EncodePersistedObject(ctx, nil)
	require.NoError(t, err)
	require.Contains(t, string(encoded.JSON), checkpointID)
	require.Contains(t, string(encoded.JSON), `"workspaceEnv":"dev"`)
	require.Contains(t, string(encoded.JSON), `"gitOrigin":"github.com/acme/repo"`)
	decoded, err := (&Workspace{}).DecodePersistedObject(ctx, nil, 0, nil, encoded.JSON)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/repo", decoded.(*Workspace).GitOrigin())
	require.NotContains(t, string(encoded.JSON), "live-client")
	require.NotContains(t, string(encoded.JSON), "/checkout")

	// A workspace with no checkpoint identity is unaffected: it is its own
	// destination, and never consults the retained origins.
	local := &Workspace{ClientID: "local-client"}
	local.SetSource(NewWorkspaceSourceClientLocal("/work"))
	clientID, hostPath, err = local.ExportTarget(ctx)
	require.NoError(t, err)
	require.Equal(t, "local-client", clientID)
	require.Equal(t, "/work", hostPath)
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
