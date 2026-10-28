package dagql

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestSchemaASTCacheBuildsOncePerView(t *testing.T) {
	cache := &SchemaASTCache{}
	var builds atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for _, view := range []call.View{"one", "two"} {
				entry := cache.get(view, func(call.View) (*ast.Schema, digest.Digest) {
					builds.Add(1)
					return &ast.Schema{}, digest.FromString(string(view))
				})
				require.Equal(t, digest.FromString(string(view)), entry.digest)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 2, builds.Load())
}

func BenchmarkSchemaASTAcrossClientForks(b *testing.B) {
	parent := newDagqlServerForTest(b, &reflectedAccessorTestObject{})
	for i := range 1000 {
		parent.Root().ObjectType().Extend(FieldSpec{
			Name: fmt.Sprintf("field%d", i), Type: String(""), Description: "A schema field.",
		}, nil)
	}
	for _, shared := range []bool{false, true} {
		b.Run(fmt.Sprintf("shared=%t", shared), func(b *testing.B) {
			cache := &SchemaASTCache{}
			b.ReportAllocs()
			for b.Loop() {
				client, err := parent.Fork(b.Context(), &reflectedAccessorTestObject{})
				if err != nil {
					b.Fatal(err)
				}
				if shared {
					client.UseSchemaASTCache(cache)
				}
				if client.SchemaDigest() == "" {
					b.Fatal("empty schema digest")
				}
			}
		})
	}
}

func TestSharedSchemaASTRetainsIndependentServers(t *testing.T) {
	root := &reflectedAccessorTestObject{}
	a := newDagqlServerForTest(t, root)
	add := func(s *Server, name, description string) {
		s.Root().ObjectType().Extend(FieldSpec{Name: name, Type: String(""), Description: description}, nil)
	}
	add(a, "baseField", "base")
	b, err := a.Fork(context.Background(), &reflectedAccessorTestObject{})
	require.NoError(t, err)
	cache := &SchemaASTCache{}
	a.UseSchemaASTCache(cache)
	b.UseSchemaASTCache(cache)
	before := a.Schema()
	require.Same(t, before, b.Schema())
	require.Equal(t, a.SchemaDigest(), b.SchemaDigest())
	require.NotSame(t, a.Root().(ObjectResult[*reflectedAccessorTestObject]).Self(), b.Root().(ObjectResult[*reflectedAccessorTestObject]).Self())

	// Updating one fork cannot change the schema or cache of the other.
	add(b, "forkField", "fork")
	require.NotSame(t, before, b.Schema())
	require.NotNil(t, b.Schema().Query.Fields.ForName("forkField"))
	require.Same(t, before, a.Schema())
	require.Nil(t, a.Schema().Query.Fields.ForName("forkField"))
	require.NotEqual(t, a.SchemaDigest(), b.SchemaDigest())
}

func TestSharedSchemaASTKeepsViewsSeparate(t *testing.T) {
	a := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
	a.Root().ObjectType().Extend(FieldSpec{Name: "versioned", Type: String(""), ViewFilter: ExactView("visible")}, nil)
	b, err := a.Fork(t.Context(), &reflectedAccessorTestObject{})
	require.NoError(t, err)
	cache := &SchemaASTCache{}
	a.UseSchemaASTCache(cache)
	b.UseSchemaASTCache(cache)
	require.Same(t, a.SchemaForView("visible"), b.SchemaForView("visible"))
	require.Same(t, a.SchemaForView("hidden"), b.SchemaForView("hidden"))
	require.NotSame(t, a.SchemaForView("visible"), b.SchemaForView("hidden"))
	require.NotNil(t, a.SchemaForView("visible").Query.Fields.ForName("versioned"))
	require.Nil(t, b.SchemaForView("hidden").Query.Fields.ForName("versioned"))
}
