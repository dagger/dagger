package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

type cacheTestServer struct {
	mockServer
	cache *dagql.SessionCache
}

func (s *cacheTestServer) Cache(context.Context) (*dagql.SessionCache, error) {
	return s.cache, nil
}

func (s *cacheTestServer) CurrentServedDeps(context.Context) (*ServedMods, error) {
	return &ServedMods{}, nil
}

func newSessionCacheForTest(t *testing.T) *dagql.SessionCache {
	t.Helper()

	baseCache, err := dagql.NewCache(t.Context(), "")
	require.NoError(t, err)
	return dagql.NewSessionCache(baseCache)
}

func TestModDepsSchemaUsesCurrentSessionCache(t *testing.T) {
	initialCache := newSessionCacheForTest(t)
	currentCache := newSessionCacheForTest(t)
	root := &Query{Server: &cacheTestServer{cache: currentCache}}

	deps := &ModDeps{
		root:               root,
		lazilyLoadedSchema: dagql.NewServer(root, initialCache),
	}

	dag, err := deps.Schema(t.Context())
	require.NoError(t, err)
	require.Same(t, currentCache, dag.Cache)
	require.NotSame(t, deps.lazilyLoadedSchema, dag)
}

func TestServedModsSchemaUsesCurrentSessionCache(t *testing.T) {
	initialCache := newSessionCacheForTest(t)
	currentCache := newSessionCacheForTest(t)
	root := &Query{Server: &cacheTestServer{cache: currentCache}}

	mods := &ServedMods{
		root:               root,
		lazilyLoadedSchema: dagql.NewServer(root, initialCache),
	}

	dag, err := mods.Schema(t.Context())
	require.NoError(t, err)
	require.Same(t, currentCache, dag.Cache)
	require.NotSame(t, mods.lazilyLoadedSchema, dag)
}
