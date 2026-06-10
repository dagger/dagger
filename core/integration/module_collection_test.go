package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

// CollectionSuite enforces the module collection contract from
// hack/designs/modules-v2/collections.md (branch modules-v2):
//
//   - the public projection: collection types expose exactly
//     keys/list/get(key)/subset(keys)/batch, and nothing of the raw
//     module-defined shape ("DagQL Schema" + "Batch Namespace" sections)
//   - the collection algebra laws ("Collection Algebra" section)
//   - generated clients consume the projected surface ("Generated Clients")
//   - collection state survives engine caching across sessions
//   - non-string key types round-trip through every layer
//
// The CLI selection surface (dagger list, filters) is covered separately in
// workspace_artifacts_test.go.
type CollectionSuite struct{}

func TestCollection(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CollectionSuite{})
}

// zooWorkspace is the canonical collection fixture. The shape is chosen so
// that one fixture exercises every part of the contract:
//
//   - Animals is keyed by name, with deterministic key order
//   - Prefix is backing-only state: it must be hidden from the public
//     surface, but must still flow into get results (and survive subset)
//   - Tag is neither keys nor get, so it must be re-homed under batch; it
//     takes an argument and reads Keys, proving batch operations see the
//     current (possibly narrowed) subset
const zooModuleSource = `package main

import "strings"

type Zoo struct{}

// The animals living in this zoo
func (z *Zoo) Animals() *Animals {
	return &Animals{Prefix: "zoo", Keys: []string{"lion", "tiger", "bear"}}
}

// A collection of animals, keyed by name
// +collection
type Animals struct {
	Prefix string
	Keys   []string
}

// Look up one animal by name
func (a *Animals) Get(name string) *Animal {
	return &Animal{Name: a.Prefix + "/" + name}
}

// Tag every animal in the current subset
func (a *Animals) Tag(suffix string) string {
	return strings.Join(a.Keys, ",") + ":" + suffix
}

// A single animal
type Animal struct {
	Name string
}
`

func zooWorkspace(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile("dagger.toml", `[modules.zoo]
source = "zoo"
`).
		WithNewFile("zoo/dagger.json", `{"name": "zoo", "sdk": "go", "source": "."}`).
		WithNewFile("zoo/main.go", zooModuleSource)
}

// TestPublicSurface is the projection contract made executable: what every
// client (CLI, shell, codegen, introspection) sees of a collection type. If
// this test fails, the typedef projection and the served schema have
// diverged, and every downstream surface is suspect.
func (CollectionSuite) TestPublicSurface(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := zooWorkspace(t, c).
		With(daggerQuery(`{
			currentTypeDefs(hideCore: true) {
				asObject {
					name
					fields { name }
					functions { name args { name } }
				}
				asCollection {
					keyType { kind }
					valueType { asObject { name } }
					batchType { asObject { name functions { name } } }
				}
			}
		}`)).
		Stdout(ctx)
	require.NoError(t, err)

	var res struct {
		CurrentTypeDefs []struct {
			AsObject *struct {
				Name      string
				Fields    []struct{ Name string }
				Functions []struct {
					Name string
					Args []struct{ Name string }
				}
			}
			AsCollection *struct {
				KeyType   struct{ Kind string }
				ValueType struct {
					AsObject struct{ Name string }
				}
				BatchType struct {
					AsObject struct {
						Name      string
						Functions []struct{ Name string }
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res))

	memberNames := func(items []struct{ Name string }) []string {
		names := make([]string, len(items))
		for i, item := range items {
			names[i] = item.Name
		}
		return names
	}

	typeNames := []string{}
	for _, def := range res.CurrentTypeDefs {
		if def.AsObject == nil {
			continue
		}
		typeNames = append(typeNames, def.AsObject.Name)

		if def.AsObject.Name != "ZooAnimals" {
			continue
		}
		obj := def.AsObject

		// The full public surface, and nothing else: the backing Prefix
		// field and the raw Tag function must not leak.
		require.Equal(t, []string{"keys"}, memberNames(obj.Fields))
		fnNames := make([]string, len(obj.Functions))
		argNames := map[string][]string{}
		for i, fn := range obj.Functions {
			fnNames[i] = fn.Name
			for _, arg := range fn.Args {
				argNames[fn.Name] = append(argNames[fn.Name], arg.Name)
			}
		}
		require.ElementsMatch(t, []string{"list", "get", "subset", "batch"}, fnNames)
		require.Equal(t, []string{"key"}, argNames["get"])
		require.Equal(t, []string{"keys"}, argNames["subset"])

		require.NotNil(t, def.AsCollection, "projected type must keep its collection metadata")
		require.Equal(t, "STRING_KIND", def.AsCollection.KeyType.Kind)
		require.Equal(t, "ZooAnimal", def.AsCollection.ValueType.AsObject.Name)
		require.Equal(t, "ZooAnimalsBatch", def.AsCollection.BatchType.AsObject.Name)
		require.Equal(t, []string{"tag"}, memberNames(def.AsCollection.BatchType.AsObject.Functions))
	}

	require.Contains(t, typeNames, "ZooAnimals")
	// Clients resolve the batch return type by name, so the synthetic batch
	// type must be listed as a type definition in its own right.
	require.Contains(t, typeNames, "ZooAnimalsBatch")
}

// TestAlgebraLaws pins the collection algebra from collections.md in a
// single query against a single golden document:
//
//   - list returns items in keys order
//   - subset is exact and parent-ordered (asked for bear,lion; parent order
//     is lion,bear)
//   - backing state (Prefix) flows into get results and survives subset
func (CollectionSuite) TestAlgebraLaws(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := zooWorkspace(t, c).
		With(daggerQuery(`{
			zoo {
				animals {
					keys
					list { name }
					subset(keys: ["bear", "lion"]) {
						keys
						list { name }
					}
				}
			}
		}`)).
		Stdout(ctx)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"zoo": {
			"animals": {
				"keys": ["lion", "tiger", "bear"],
				"list": [
					{"name": "zoo/lion"},
					{"name": "zoo/tiger"},
					{"name": "zoo/bear"}
				],
				"subset": {
					"keys": ["lion", "bear"],
					"list": [
						{"name": "zoo/lion"},
						{"name": "zoo/bear"}
					]
				}
			}
		}
	}`, out)
}

// TestGeneratedClients proves a dependent module can drive a collection
// through its generated client: the projected members must exist under
// their generated Go names and dispatch correctly at runtime. A naming or
// typedef regression fails this test with the generated client's compile
// error in the output.
func (CollectionSuite) TestGeneratedClients(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := zooWorkspace(t, c).
		WithNewFile("keeper/dagger.json",
			`{"name": "keeper", "sdk": "go", "source": ".", "dependencies": [{"name": "zoo", "source": "../zoo"}]}`).
		WithNewFile("keeper/main.go", `package main

import "context"

type Keeper struct{}

// Look up one animal through the zoo's collection
func (k *Keeper) Mascot(ctx context.Context) (string, error) {
	return dag.Zoo().Animals().Get("lion").Name(ctx)
}

// Tag a narrowed subset through the zoo's batch namespace
func (k *Keeper) Roster(ctx context.Context) (string, error) {
	return dag.Zoo().Animals().Subset([]string{"bear"}).Batch().Tag(ctx, "fed")
}
`).
		WithNewFile("dagger.toml", `[modules.zoo]
source = "zoo"

[modules.keeper]
source = "keeper"
`).
		With(daggerQuery(`{keeper {mascot roster}}`)).
		Stdout(ctx)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"keeper": {
			"mascot": "zoo/lion",
			"roster": "bear:fed"
		}
	}`, out)
}

// TestCacheReplay runs the same collection chain in two separate sessions
// against the same engine. The second run resolves through the engine cache,
// which exercises the persisted encoding of collection metadata (including
// the backing type used for SDK dispatch). A persistence regression
// typically passes every cold-run test and only breaks here.
func (CollectionSuite) TestCacheReplay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const query = `{
		zoo {
			animals {
				subset(keys: ["tiger", "bear"]) { keys }
				get(key: "lion") { name }
			}
		}
	}`
	const expected = `{
		"zoo": {
			"animals": {
				"subset": {"keys": ["tiger", "bear"]},
				"get": {"name": "zoo/lion"}
			}
		}
	}`

	firstRun := zooWorkspace(t, c).With(daggerQuery(query))
	out, err := firstRun.Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, expected, out)

	// The env variable changes the container state so the second exec is not
	// itself a cached copy of the first: it must start a fresh dagger session
	// and replay the chain through the engine cache.
	out, err = firstRun.
		WithEnvVariable("CACHEBUSTER", "second-session").
		With(daggerQuery(query)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, expected, out)
}

// TestKeyTypes covers non-string collection keys. Keys cross several
// representation layers (GraphQL literals, SDK inputs, JSON identity for
// subset membership, string coordinates for dagger list), and each layer
// has its own conversion: a mismatch in any of them shows up here.
func (CollectionSuite) TestKeyTypes(_ context.Context, t *testctx.T) {
	t.Run("integer keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			WithNewFile("dagger.toml", `[modules.ci]
source = "ci"
`).
			WithNewFile("ci/dagger.json", `{"name": "ci", "sdk": "go", "source": "."}`).
			WithNewFile("ci/main.go", `package main

type Ci struct{}

// The builds tracked by this CI
func (c *Ci) Builds() *Builds {
	return &Builds{Keys: []int{101, 102}}
}

// A collection of builds, keyed by build number
// +collection
type Builds struct {
	Keys []int
}

// Look up one build by number
func (b *Builds) Get(number int) *Build {
	return &Build{Number: number}
}

// A single build
type Build struct {
	Number int
}
`)

		out, err := base.
			With(daggerQuery(`{
				ci {
					builds {
						keys
						subset(keys: [102]) { keys }
						get(key: 101) { number }
					}
				}
			}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"ci": {
				"builds": {
					"keys": [101, 102],
					"subset": {"keys": [102]},
					"get": {"number": 101}
				}
			}
		}`, out)

		// Integer keys stringify as artifact coordinates.
		out, err = base.
			With(daggerExecRaw("list", "ci-build")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"101", "102"}, strings.Fields(out))
	})

	t.Run("enum keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			WithNewFile("dagger.toml", `[modules.palette]
source = "palette"
`).
			WithNewFile("palette/dagger.json", `{"name": "palette", "sdk": "go", "source": "."}`).
			WithNewFile("palette/main.go", `package main

type Color string

const (
	Red  Color = "RED"
	Blue Color = "BLUE"
)

type Palette struct{}

// The swatches in this palette
func (p *Palette) Swatches() *Swatches {
	return &Swatches{Keys: []Color{Red, Blue}}
}

// A collection of swatches, keyed by color
// +collection
type Swatches struct {
	Keys []Color
}

// Look up one swatch by color
func (s *Swatches) Get(color Color) *Swatch {
	return &Swatch{Color: color}
}

// A single swatch
type Swatch struct {
	Color Color
}
`)

		out, err := base.
			With(daggerQuery(`{
				palette {
					swatches {
						keys
						subset(keys: [BLUE]) { keys }
						get(key: RED) { color }
					}
				}
			}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"palette": {
				"swatches": {
					"keys": ["RED", "BLUE"],
					"subset": {"keys": ["BLUE"]},
					"get": {"color": "RED"}
				}
			}
		}`, out)

		// Enum keys stringify as their member names in artifact coordinates.
		// Listing order is scope order (sorted coordinates), not keys order.
		out, err = base.
			With(daggerExecRaw("list", "palette-swatch")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"BLUE", "RED"}, strings.Fields(out))
	})
}
