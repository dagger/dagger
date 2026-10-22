package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"
	"golang.org/x/sync/errgroup"
)

func schemaCacheContext(t testing.TB) context.Context {
	t.Helper()
	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "schema-test", func() {},
		func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			return engine.NewClientLifecycleLease(kind, owner, func() {}, nil), nil
		})
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "schema-test", ClientID: "schema-client"}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	t.Cleanup(func() {
		require.NoError(t, cache.ReleaseSession(ctx, "schema-test"))
		require.NoError(t, cache.Close(ctx))
	})
	return ctx
}

func BenchmarkDangSchemaDecode(b *testing.B) {
	data, err := os.ReadFile("../../../../cmd/codegen/introspection/testdata/schema.json")
	require.NoError(b, err)
	var response struct {
		Schema json.RawMessage `json:"__schema"`
	}
	// The fixture contains the schema object; SDK files contain a response.
	response.Schema = data
	data, err = json.Marshal(response)
	require.NoError(b, err)
	read := func(context.Context) (*introspection.Schema, error) {
		var response introspection.Response
		err := json.NewDecoder(bytes.NewReader(data)).Decode(&response)
		return response.Schema, err
	}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%v", cached), func(b *testing.B) {
			ctx := context.Background()
			load := read
			if cached {
				ctx = schemaCacheContext(b)
				load = func(ctx context.Context) (*introspection.Schema, error) {
					return cachedDangSchema(ctx, "schema-fixture", read)
				}
			}
			schema, err := load(ctx)
			require.NoError(b, err)
			require.NotNil(b, schema)
			require.NotEmpty(b, schema.Types)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := load(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDangSchemaCacheIsolationAndRetry(t *testing.T) {
	ctx := schemaCacheContext(t)
	var reads atomic.Int32
	read := func(context.Context) (*introspection.Schema, error) {
		reads.Add(1)
		var response introspection.Response
		err := json.Unmarshal([]byte(`{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query"}]}}`), &response)
		return response.Schema, err
	}
	first, err := cachedDangSchema(ctx, "file-one", read)
	require.NoError(t, err)
	second, err := cachedDangSchema(ctx, "file-one", read)
	require.NoError(t, err)
	require.EqualValues(t, 1, reads.Load())
	require.NotSame(t, first, second)
	require.Same(t, first.Types[0], second.Types[0], "immutable decoded metadata is reused")
	first.Types = append(first.Types, &introspection.Type{Name: "FirstModule"})
	second.Types = append(second.Types, &introspection.Type{Name: "SecondModule"})
	again, err := cachedDangSchema(ctx, "file-one", read)
	require.NoError(t, err)
	require.Len(t, again.Types, 1, "module-local types must not enter the template")
	require.Equal(t, "FirstModule", first.Types[1].Name)
	require.Equal(t, "SecondModule", second.Types[1].Name)

	other, err := cachedDangSchema(ctx, "file-two", read)
	require.NoError(t, err)
	require.NotSame(t, again.Types[0], other.Types[0], "another immutable file has its own schema")
	require.EqualValues(t, 2, reads.Load())
	failure := errors.New("read failed")
	_, err = cachedDangSchema(ctx, "retry", func(context.Context) (*introspection.Schema, error) { return nil, failure })
	require.ErrorIs(t, err, failure)
	_, err = cachedDangSchema(ctx, "retry", read)
	require.NoError(t, err, "an unsuccessful decode must not poison later calls")

	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, cache.EntryStats().CompletedArbitrary)
	require.NoError(t, cache.ReleaseSession(ctx, "schema-test"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "schema-test"))
	require.Zero(t, cache.EntryStats().CompletedArbitrary, "decoded schemas must leave with their last session")
	require.Zero(t, cache.EntryStats().OngoingArbitrary)
}

type schemaCacheClient string

func (client schemaCacheClient) MakeRequest(_ context.Context, _ *graphql.Request, response *graphql.Response) error {
	data, err := json.Marshal(map[string]string{"value": string(client)})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, response.Data)
}

func TestDangSchemaCacheUsesCurrentClients(t *testing.T) {
	ctx := schemaCacheContext(t)
	template, err := dang.SchemaFromSDL(`
directive @hint(message: String = "default") on FIELD_DEFINITION
enum Choice { FIRST SECOND }
input Options { count: Int = 1 }
interface Node { id: ID! }
type Item implements Node { id: ID!, name: String! }
scalar Timestamp
type Query {
  value: String! @hint
  item(options: Options): Item
  choices: [Choice!]!
  now: Timestamp
}
`, "schema.graphqls")
	require.NoError(t, err)
	before, err := json.Marshal(template)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte("import Session\nresult: String! = Session.value\n"), 0o600))
	var reads atomic.Int32
	read := func(ctx context.Context) (*introspection.Schema, error) {
		reads.Add(1)
		return template, ctx.Err()
	}
	var group errgroup.Group
	for i := range 8 {
		group.Go(func() error {
			schema, err := cachedDangSchema(ctx, "shared-file", read)
			if err != nil {
				return err
			}
			name := fmt.Sprintf("client-%d", i)
			callCtx := dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient(name)})
			env, err := dang.RunDir(callCtx, dir, false)
			if err != nil {
				return err
			}
			value, found, err := env.Lookup(callCtx, "result")
			if err != nil {
				return fmt.Errorf("lookup result: %w", err)
			}
			if !found {
				return errors.New("result was not bound")
			}
			if value.(dang.StringValue).Val != name {
				return fmt.Errorf("wrong client value: %v; want %s", value, name)
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())
	require.EqualValues(t, 1, reads.Load())
	after, err := json.Marshal(template)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "evaluation must not mutate shared schema metadata")
}
