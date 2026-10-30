package core

import (
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestWorkspaceProvenanceRecipe(t *testing.T) {
	ws := call.New().Append(&ast.Type{NamedType: "Workspace", NonNull: true}, "currentWorkspace")
	read := ws.Append(&ast.Type{NamedType: "Directory", NonNull: true}, "directory", call.WithArgs(
		call.NewArgument("path", call.NewLiteralString("/docs"), false),
		call.NewArgument("include", call.NewLiteralList(call.NewLiteralString("**/*.md")), false),
	))
	// A transformation must retain the original workspace coordinates.
	transformed := read.Append(&ast.Type{NamedType: "Directory", NonNull: true}, "withNewFile", call.WithArgs(call.NewArgument("path", call.NewLiteralString("generated.txt"), false)))
	got := provenanceFromRecipe(transformed)
	require.Equal(t, []provenance.Selector{{Path: "/docs", Include: []string{"**/*.md"}, Exclude: []string{}, Kind: provenance.WorkspaceRead}}, got)
	// Absolute paths can be inspected. Relative reads cannot be resolved from
	// a recipe alone without loading its workspace's Cwd.
	relative := ws.Append(&ast.Type{NamedType: "File", NonNull: true}, "file", call.WithArgs(call.NewArgument("path", call.NewLiteralString("config.json"), false)))
	got = provenanceFromRecipe(relative)
	require.Equal(t, []provenance.Selector{{Path: "/", Include: []string{}, Exclude: []string{}, Conservative: true, Kind: provenance.WorkspaceRead}}, got)
	// Traverse IDs nested in object/list inputs and implicit inputs.
	constructed := call.New().Append(&ast.Type{NamedType: "App", NonNull: true}, "app", call.WithImplicitInputs(call.NewArgument("inputs", call.NewLiteralObject(call.NewArgument("files", call.NewLiteralList(call.NewLiteralID(transformed)), false)), false)))
	require.Equal(t, provenanceFromRecipe(transformed), provenanceFromRecipe(constructed))
	require.True(t, provenanceFromRecipe(ws)[0].Conservative)
}

func TestWorkspaceProvenanceUnknownArtifact(t *testing.T) {
	// No tree or workspace: inspection must retain unknown inputs without
	// trying to call the artifact or requiring an engine connection.
	p, err := (&Artifact{}).Provenance(t.Context())
	require.NoError(t, err)
	require.Len(t, p.Selectors, 2)
	match, err := p.AffectedByPath("unrelated/file", nil)
	require.NoError(t, err)
	require.True(t, match)
	_, err = p.AffectedByPath("file", []string{"INVALID"})
	require.Error(t, err)
}

func TestWorkspaceProvenancePersistence(t *testing.T) {
	original := newWorkspaceProvenance([]provenance.Selector{conservativeProvenance(provenance.WorkspaceRead)})
	encoded, err := original.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	decoded, err := original.DecodePersistedObject(t.Context(), nil, encoded.JSON)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}
