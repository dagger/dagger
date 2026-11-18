package core

import (
	"context"
	"encoding/json"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
)

// A fresh engine finds the Go builds every Go module load reads first in the
// entries its image ships, and builds only the module's own packages. Each
// build is a result of Directory.withPlatform (dagger/go declares a build as
// made for the platform it compiles for): a new built-in Go SDK module's first
// call on an engine started from the image makes 3 of them, where an engine
// without the entries makes every dependency's, about 500.
func (CacheSuite) TestImageEntriesServeGoModuleBuilds(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	devEngine := devEngineContainerAsService(devEngineContainer(c))
	mod := core.NewQuery(c).Directory().
		WithNewFile("dagger.json", `{"name": "test", "engineVersion": "latest", "sdk": {"source": "go"}}`).
		WithNewFile("main.go", `package main

type Test struct{}

func (m *Test) Hello() string { return "hi" }
`)
	ran := engineClientContainer(ctx, t, c, devEngine).
		WithMountedDirectory("/work", mod).
		WithWorkdir("/work").
		WithExec([]string{"sh", "-ec", `
q='{ engine { localCache { entrySet { entries { dagqlCall createdTimeUnixNano } } } } }'
echo "$q" | dagger query > /before.json
dagger call hello
echo "$q" | dagger query > /after.json
`}, core.ContainerWithExecOpts{DisableDaggerInDagger: true})
	out, err := ran.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hi")

	entries := func(path string) []imageEntriesCacheEntry {
		raw, err := ran.File(path).Contents(ctx)
		require.NoError(t, err)
		var res struct {
			Engine struct {
				LocalCache struct {
					EntrySet struct {
						Entries []imageEntriesCacheEntry
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &res))
		return res.Engine.LocalCache.EntrySet.Entries
	}
	var started int64
	for _, e := range entries("/before.json") {
		started = max(started, e.created(t))
	}
	var builds int
	for _, e := range entries("/after.json") {
		if e.DagqlCall == "Directory.withPlatform" && e.created(t) > started {
			builds++
		}
	}
	require.Less(t, builds, 20, "the module's first call builds only its own packages, finding its dependencies' builds in the image's entries")
}

type imageEntriesCacheEntry struct {
	DagqlCall           string
	CreatedTimeUnixNano json.Number
}

func (e imageEntriesCacheEntry) created(t *testctx.T) int64 {
	n, err := e.CreatedTimeUnixNano.Int64()
	require.NoError(t, err)
	return n
}
