package cacheimport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/moby/buildkit/solver"
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

	require.Equal(t, len(cfg.Layers), 2)
	require.Equal(t, len(cfg.Records), 3)

	require.Equal(t, cfg.Layers[0].Blob, dgst("d0"))
	require.Equal(t, cfg.Layers[0].ParentIndex, -1)
	require.Equal(t, cfg.Layers[1].Blob, dgst("d1"))
	require.Equal(t, cfg.Layers[1].ParentIndex, 0)

	require.Equal(t, cfg.Records[0].Digest, outputKey(dgst("baz"), 0))
	require.Equal(t, len(cfg.Records[0].Inputs), 2)
	require.Equal(t, len(cfg.Records[0].Results), 1)

	require.Equal(t, cfg.Records[1].Digest, outputKey(dgst("foo"), 0))
	require.Equal(t, len(cfg.Records[1].Inputs), 0)
	require.Equal(t, len(cfg.Records[1].Results), 0)

	require.Equal(t, cfg.Records[2].Digest, outputKey(dgst("bar"), 1))
	require.Equal(t, len(cfg.Records[2].Inputs), 0)
	require.Equal(t, len(cfg.Records[2].Results), 0)

	require.Equal(t, cfg.Records[0].Results[0].LayerIndex, 1)
	require.Equal(t, cfg.Records[0].Inputs[0][0].Selector, "")
	require.Equal(t, cfg.Records[0].Inputs[0][0].LinkIndex, 1)
	require.Equal(t, cfg.Records[0].Inputs[1][0].Selector, "sel0")
	require.Equal(t, cfg.Records[0].Inputs[1][0].LinkIndex, 2)

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

	require.Equal(t, len(cfg.Layers), 2)
	require.Equal(t, len(cfg.Records), 4)
}

func dgst(s string) digest.Digest {
	return digest.FromBytes([]byte(s))
}
