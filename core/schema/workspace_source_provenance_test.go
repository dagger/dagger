package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

type workspaceSourceMetadataTestServer struct {
	*currentTypeDefsTestServer
	current     *core.Workspace
	engineCalls int
}

func (s *workspaceSourceMetadataTestServer) CurrentWorkspace(context.Context) (*core.Workspace, error) {
	return s.current, nil
}

func (s *workspaceSourceMetadataTestServer) Engine(context.Context) (*engineutil.Client, error) {
	s.engineCalls++
	return nil, errors.New("metadata constructor must not open the filesystem engine")
}

// Inspect captured facts from the real schema observer, rather than repeating
// the operation classifier's list in the test.
type workspaceSourceMetadataFact struct {
	Complete             bool
	CapabilitiesComplete bool
	Issues               []string
	Prepared             workspaceSourceMetadataSummary
	ContextInputs        workspaceSourceMetadataSummary
	Reads                []struct {
		Source workspaceSourceMetadataRegion
	}
	Retained []struct {
		Output []string
	}
}

type workspaceSourceMetadataSummary struct {
	Issues  []string
	Sources []workspaceSourceMetadataRegion
}

type workspaceSourceMetadataRegion struct {
	Selectors []provenance.Selector
	Issues    []string
}

func TestWorkspaceMetadataConstructorsDoNotRead(t *testing.T) {
	const session = "schema-workspace-metadata"
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID: "metadata-client", SessionID: session,
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	server := &workspaceSourceMetadataTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{
		platform: core.Platform{OS: "linux", Architecture: "amd64"},
	}}
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	root := core.NewRoot(server)
	srv, err := base.Fork(ctx, root, "v1.0.0-beta.28")
	require.NoError(t, err)
	server.dag = srv
	ctx = core.ContextWithQuery(ctx, root)

	rowID := func(t *testing.T, value dagql.AnyResult) uint64 {
		t.Helper()
		id, err := value.ID()
		require.NoError(t, err)
		require.NotZero(t, id.EngineResultID())
		return id.EngineResultID()
	}
	inspect := func(t *testing.T, value dagql.AnyResult) []workspaceSourceMetadataFact {
		t.Helper()
		facts, complete, err := cache.ResultFacts(ctx, session,
			&dagql.ResultCallRef{ResultID: rowID(t, value)}, "workspace-inputs/v7")
		require.NoError(t, err)
		require.True(t, complete)
		require.NotEmpty(t, facts, "absence of facts is not proof of no reads")
		var decoded []workspaceSourceMetadataFact
		for _, record := range facts {
			var fact workspaceSourceMetadataFact
			require.NoError(t, json.Unmarshal(record.Payload, &fact))
			require.True(t, fact.Complete)
			require.True(t, fact.CapabilitiesComplete)
			require.Empty(t, fact.Issues)
			regions := append([]workspaceSourceMetadataRegion{}, fact.Prepared.Sources...)
			regions = append(regions, fact.ContextInputs.Sources...)
			require.Empty(t, fact.Prepared.Issues)
			require.Empty(t, fact.ContextInputs.Issues)
			for _, read := range fact.Reads {
				regions = append(regions, read.Source)
			}
			for _, region := range regions {
				require.Empty(t, region.Issues)
				for _, selector := range region.Selectors {
					require.NotEqual(t, provenance.WorkspaceRead, selector.Kind,
						"metadata access must not invent a filesystem read: %+v", selector)
				}
			}
			decoded = append(decoded, fact)
		}
		return decoded
	}

	for _, owner := range []string{"first-owner", "second-owner"} {
		t.Run(owner, func(t *testing.T) {
			server.current = &core.Workspace{Address: "file:///" + owner, ClientID: owner, Cwd: "."}
			server.current.SetSource(core.NewWorkspaceSourceClientLocal("/" + owner))
			var workspace dagql.ObjectResult[*core.Workspace]
			require.NoError(t, srv.Select(ctx, srv.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}))
			workspaceID := rowID(t, workspace)

			selectSDK := func() dagql.ObjectResult[*core.WorkspaceSDK] {
				var sdk dagql.ObjectResult[*core.WorkspaceSDK]
				require.NoError(t, srv.Select(ctx, workspace, &sdk, dagql.Selector{Field: "__workspaceSDK", Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String("metadata-sdk")},
					{Name: "ref", Value: dagql.String("./sdk")},
					{Name: "moduleNames", Value: dagql.ArrayInput[dagql.String]{"authored-module"}},
					{Name: "moduleSources", Value: dagql.ArrayInput[dagql.String]{"./authored"}},
					{Name: "clientNames", Value: dagql.ArrayInput[dagql.String]{"generated-client"}},
					{Name: "clientSources", Value: dagql.ArrayInput[dagql.String]{"./client"}},
				}}))
				return sdk
			}
			sdk := selectSDK()
			require.Equal(t, workspaceID, rowID(t, sdk.Self().Workspace), "the stored capability keeps its actual producer")
			require.Equal(t, owner, sdk.Self().Workspace.Self().ClientID)
			facts := inspect(t, sdk)
			retainsWorkspace := false
			for _, fact := range facts {
				for _, retained := range fact.Retained {
					if len(retained.Output) == 1 && retained.Output[0] == "field:workspace" {
						retainsWorkspace = true
					}
				}
			}
			require.True(t, retainsWorkspace, "no read must not discard the retained Workspace capability")
			hit := selectSDK()
			require.True(t, hit.HitCache())
			require.Equal(t, rowID(t, sdk), rowID(t, hit))
			require.Equal(t, workspaceID, rowID(t, hit.Self().Workspace))
			inspect(t, hit)

			entryIDs := map[string]uint64{}
			for use, parent := range []dagql.ObjectResult[*core.WorkspaceSDK]{sdk, hit} {
				for _, list := range []struct{ field, name string }{
					{"modules", "authored-module"}, {"clients", "generated-client"},
				} {
					var entries dagql.ObjectResultArray[*core.WorkspaceModule]
					require.NoError(t, srv.Select(ctx, parent, &entries, dagql.Selector{Field: list.field}))
					require.Len(t, entries, 1)
					entry := entries[0]
					require.Equal(t, list.name, entry.Self().Name)
					if use == 0 {
						entryIDs[list.field] = rowID(t, entry)
					} else {
						require.Equal(t, entryIDs[list.field], rowID(t, entry))
					}
					for _, fact := range inspect(t, entry) {
						require.Empty(t, fact.Retained, "SDK list metadata must not acquire its parent's Workspace capability")
					}
					name, err := entry.Select(ctx, srv, dagql.Selector{Field: "name"})
					require.NoError(t, err)
					nameValue, ok := dagql.UnwrapAs[dagql.String](name)
					require.True(t, ok)
					require.Equal(t, dagql.String(list.name), nameValue)
					if use != 0 {
						require.True(t, name.HitCache())
					}
					inspect(t, name)
				}
			}

			var module dagql.ObjectResult[*core.WorkspaceModule]
			require.NoError(t, srv.Select(ctx, workspace, &module, dagql.Selector{Field: "__workspaceModule", Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("metadata-module")},
				{Name: "main", Value: dagql.Boolean(false)},
				{Name: "source", Value: dagql.String("./module")},
			}}))
			frame, err := module.ResultCall()
			require.NoError(t, err)
			require.NotNil(t, frame.Receiver)
			require.Equal(t, workspaceID, frame.Receiver.ResultID)
			inspect(t, module)
			for _, value := range []dagql.AnyObjectResult{sdk, module} {
				name, err := value.Select(ctx, srv, dagql.Selector{Field: "name"})
				require.NoError(t, err)
				nameValue, ok := dagql.UnwrapAs[dagql.String](name)
				require.True(t, ok)
				require.NotEmpty(t, nameValue)
				inspect(t, name)
			}
		})
	}
	require.Zero(t, server.engineCalls, "constructor and scalar access must perform no engine filesystem operation")
}
