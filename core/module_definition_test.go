package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// moduleDefinitionTestModule is a definition-only module with one object
// typedef, the shape ModuleSource._moduleDefinition returns.
func moduleDefinitionTestModule(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session string) dagql.ObjectResult[*Module] {
	t.Helper()
	objDef := attachTransferObject(t, ctx, cache, srv, session, "definition-object", NewObjectTypeDef("Holder", "a holder", nil))
	typeDef := attachTransferObject(t, ctx, cache, srv, session, "definition-typedef", (&TypeDef{}).WithObject(objDef))
	def := &Module{NameField: "demo", Description: "the definition", ObjectDefs: dagql.ObjectResultArray[*TypeDef]{typeDef}}
	return attachTransferObject(t, ctx, cache, srv, session, "definition", def)
}

func moduleDefinitionTestClasses(srv *dagql.Server) {
	installModuleObjectTestModuleClass(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*TypeDef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ObjectTypeDef]{}))
}

// The Definition reference is attached as an owned dependency, encoded and
// decoded beside the runtime reference, and relocated by the module's
// persisted visitor.
func TestModuleDefinitionReferenceWiring(t *testing.T) {
	t.Parallel()
	ctx, cache, srv := containerPersistenceTestCache(t, filepath.Join(t.TempDir(), "a.db"), newContainerPersistenceTestSnapshots(), "a")
	moduleDefinitionTestClasses(srv)
	def := moduleDefinitionTestModule(t, ctx, cache, srv, "a")
	mod := &Module{NameField: "demo", Definition: dagql.NonNull(def)}

	// Attachment reports the definition as an owned dependency.
	var seen []dagql.AnyResult
	owned, err := mod.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		seen = append(seen, res)
		return res, nil
	})
	require.NoError(t, err)
	require.Len(t, seen, 1)
	require.Same(t, def.Unwrap(), seen[0].Unwrap())
	require.Len(t, owned, 1)

	// The payload carries the definition's row and the codec restores it.
	modRes := attachTransferObject(t, ctx, cache, srv, "a", "module-with-definition", mod)
	defID := persistedRowID(t, cache, def)
	refs := persistedVisitedRefs(t, ctx, cache, modRes)
	require.Equal(t, defID, refs["objectJSON.definitionResultID"], "the visitor declares the definition reference")
	record, err := cache.CapturePersistedRecord(ctx, modRes)
	require.NoError(t, err)
	var payload persistedModulePayload
	require.NoError(t, dagql.UnmarshalLosslessJSON(record.Envelope.ObjectJSON, &payload))
	require.Equal(t, defID, payload.DefinitionResultID, "the payload names the definition's row")

	// The visitor relocates the definition's row ID like the runtime's.
	reloc := &relocationVisitor{mapping: map[uint64]uint64{record.ResultID: record.ResultID, defID: defID + 1000}}
	out, err := dagql.VisitEncodedReferences(record, reloc.visit)
	require.NoError(t, err)
	var rewritten persistedModulePayload
	require.NoError(t, dagql.UnmarshalLosslessJSON(out.Envelope.ObjectJSON, &rewritten))
	require.Equal(t, defID+1000, rewritten.DefinitionResultID)
}

// A module exported through its own row carries its definition and the
// definition's typedefs, and the importing cache gets them under its own
// row numbers with the reference intact.
func TestModuleDefinitionTravelsInBundle(t *testing.T) {
	t.Parallel()
	ctx, a, srvA := containerPersistenceTestCache(t, filepath.Join(t.TempDir(), "a.db"), newContainerPersistenceTestSnapshots(), "a")
	moduleDefinitionTestClasses(srvA)
	def := moduleDefinitionTestModule(t, ctx, a, srvA, "a")
	modRes := attachTransferObject(t, ctx, a, srvA, "a", "module-with-definition", &Module{NameField: "demo", Definition: dagql.NonNull(def)})
	var bundle dagql.ValueBundle
	require.NoError(t, a.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{modRes}}, config.RefConfig{}, func(_ context.Context, values *dagql.ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 4, "module, definition, typedef, object typedef")

	bctx, b, srvB := containerPersistenceTestCache(t, filepath.Join(t.TempDir(), "b.db"), newContainerPersistenceTestSnapshots(), "b")
	moduleDefinitionTestClasses(srvB)
	// Padding so B's row numbers differ from A's.
	for i := range 5 {
		attachTransferObject(t, bctx, b, srvB, "b", "padding", &Module{NameField: string(rune('p' + i))})
	}
	mapping, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	require.Len(t, mapping, 1)
	loaded, err := b.LoadResultByResultID(bctx, "b", srvB, mapping[0].ResultID)
	require.NoError(t, err)
	imported := loaded.Unwrap().(*Module)
	require.True(t, imported.Definition.Valid, "the definition reference survives the transfer")
	require.NotEqual(t, persistedRowID(t, a, def), persistedRowID(t, b, imported.Definition.Value), "relocated to B's row")
	require.True(t, dagql.IsImportedResult(imported.Definition.Value))
	require.Equal(t, "the definition", imported.Definition.Value.Self().Description)
	require.Len(t, imported.Definition.Value.Self().ObjectDefs, 1)
	require.Equal(t, "Holder", imported.Definition.Value.Self().ObjectDefs[0].Self().AsObject.Value.Self().Name)
}
