package layers

import (
	"testing"

	"github.com/google/pprof/profile"
)

func sample(bytes int64, funcs ...string) *profile.Sample {
	s := &profile.Sample{Value: []int64{1, bytes}}
	for _, f := range funcs {
		s.Location = append(s.Location, &profile.Location{Line: []profile.Line{{Function: &profile.Function{Name: f}}}})
	}
	return s
}

func TestBucketSumsExactly(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "inuse_objects"}, {Type: "inuse_space"}},
		Sample: []*profile.Sample{
			sample(100, "runtime.mallocgc", "github.com/dagger/dagger/engine/clientdb.(*spanLookup).addLocked"),
			sample(200, "runtime.mallocgc", "github.com/dagger/dagger/core/sdk/dang/v2.cachedPreparedDangDir.func1", "github.com/dagger/dagger/dagql.(*Cache).GetOrInitArbitrary"),
			sample(300, "runtime.mallocgc", "github.com/dagger/dagger/dagql.(*Cache).GetOrInitArbitrary"),
			sample(400, "runtime.mallocgc", "github.com/dagger/dagger/core.buildSchema"),
			sample(500, "runtime.mallocgc", "main.somethingElse"),
		},
	}
	res, err := Bucket(p, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"spans": 100, "prepared": 200, "arbitrary": 300, "schema": 400, Other: 500}
	for _, l := range res.Layers {
		if l.Bytes != want[l.ID] {
			t.Errorf("%s: %d bytes, want %d", l.ID, l.Bytes, want[l.ID])
		}
	}
	if res.TotalBytes != 1500 {
		t.Errorf("total %d, want 1500", res.TotalBytes)
	}
}
