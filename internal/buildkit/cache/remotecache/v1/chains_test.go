package cacheimport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestSimpleMarshal(t *testing.T) {
	cc := NewCacheChains()

	addRecords := func() {
		foo := cc.Add(outputKey(dgst("foo"), 0))
		bar := cc.Add(outputKey(dgst("bar"), 1))
		baz := cc.Add(outputKey(dgst("baz"), 0))

		baz.LinkFrom(foo, 0, "")
		baz.LinkFrom(bar, 1, "sel0")
		r0 := &solver.Remote{
			Descriptors: []ocispecs.Descriptor{{
				Digest: dgst("d0"),
			}, {
				Digest: dgst("d1"),
			}},
		}
		baz.AddResult("", 0, time.Now(), r0)
	}

	addRecords()

	cfg, _, err := cc.Marshal(context.TODO())
	require.NoError(t, err)

	require.Equal(t, 2, len(cfg.Layers))
	require.Equal(t, 3, len(cfg.Records))

	require.Equal(t, cfg.Layers[0].Blob, dgst("d0"))
	require.Equal(t, -1, cfg.Layers[0].ParentIndex)
	require.Equal(t, cfg.Layers[1].Blob, dgst("d1"))
	require.Equal(t, 0, cfg.Layers[1].ParentIndex)

	require.Equal(t, cfg.Records[0].Digest, outputKey(dgst("baz"), 0))
	require.Equal(t, 2, len(cfg.Records[0].Inputs))
	require.Equal(t, 1, len(cfg.Records[0].Results))

	require.Equal(t, cfg.Records[1].Digest, outputKey(dgst("foo"), 0))
	require.Equal(t, 0, len(cfg.Records[1].Inputs))
	require.Equal(t, 0, len(cfg.Records[1].Results))

	require.Equal(t, cfg.Records[2].Digest, outputKey(dgst("bar"), 1))
	require.Equal(t, 0, len(cfg.Records[2].Inputs))
	require.Equal(t, 0, len(cfg.Records[2].Results))

	require.Equal(t, 1, cfg.Records[0].Results[0].LayerIndex)
	require.Equal(t, "", cfg.Records[0].Inputs[0][0].Selector)
	require.Equal(t, 1, cfg.Records[0].Inputs[0][0].LinkIndex)
	require.Equal(t, "sel0", cfg.Records[0].Inputs[1][0].Selector)
	require.Equal(t, 2, cfg.Records[0].Inputs[1][0].LinkIndex)

	// adding same info again doesn't produce anything extra
	addRecords()

	cfg2, descPairs, err := cc.Marshal(context.TODO())
	require.NoError(t, err)

	require.EqualValues(t, cfg, cfg2)

	// marshal roundtrip
	dt, err := json.Marshal(cfg)
	require.NoError(t, err)

	newChains := NewCacheChains()
	err = Parse(dt, descPairs, newChains)
	require.NoError(t, err)

	cfg3, _, err := cc.Marshal(context.TODO())
	require.NoError(t, err)
	require.EqualValues(t, cfg, cfg3)

	// add extra item
	cc.Add(outputKey(dgst("bay"), 0))
	cfg, _, err = cc.Marshal(context.TODO())
	require.NoError(t, err)

	require.Equal(t, 2, len(cfg.Layers))
	require.Equal(t, 4, len(cfg.Records))
}

func dgst(s string) digest.Digest {
	return digest.FromBytes([]byte(s))
}
