package dagql

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// offerMeaningOracle is what sameOfferMeaning means: reflect.DeepEqual over
// the two offers less their chain's addresses.
func offerMeaningOracle(a, b PersistedPartOffer) bool {
	a.Chain.Addresses, b.Chain.Addresses = nil, nil
	return reflect.DeepEqual(a, b)
}

// fullOffer is an offer with every field set, every slice and map holding
// something, and every pointer set, so that each can be changed alone.
func fullOffer() PersistedPartOffer {
	platform := func() *ocispecs.Platform {
		return &ocispecs.Platform{Architecture: "arm64", OS: "linux", OSVersion: "6.1", OSFeatures: []string{"sse4"}, Variant: "v8"}
	}
	// A time in a location of its own, as a parsed offset gives one.
	created := time.Date(2026, 11, 22, 18, 0, 0, 5, time.FixedZone("PST", -8*3600))
	layer := digest.FromString("layer")
	return PersistedPartOffer{
		Address: PersistedPartAddress{
			OutputPath: PersistedRefPath{{Field: "rootfs"}, {Index: 2, IsIndex: true}},
			Part:       "snapshot",
		},
		Value: SnapshotValue{
			Kind:     "directory",
			Path:     "/src",
			Platform: platform(),
			Services: []TransferredServiceBinding{{ServiceResultID: 7, Hostname: "db", Aliases: []string{"postgres"}}},
		},
		Chain: OfferedChain{
			Layers: []snapshots.ExportLayer{{
				Descriptor: ocispecs.Descriptor{
					MediaType:    "application/vnd.oci.image.layer.v1.tar+zstd",
					Digest:       layer,
					Size:         1234,
					URLs:         []string{"https://example.com/layer"},
					Annotations:  map[string]string{"a": "b"},
					Data:         []byte{1, 2},
					Platform:     platform(),
					ArtifactType: "application/x-test",
				},
				Description: "a layer",
				CreatedAt:   &created,
				Computed:    true,
			}},
			Addresses:  map[digest.Digest]BlobAddress{layer: {URL: "https://store.example/layer", ExpiresAtUnix: 42}},
			RenewalKey: "renew",
			Packs:      map[digest.Digest]digest.Digest{layer: digest.FromString("pack")},
		},
		Owner: PersistedOfferOwner{DependencyIDs: []uint64{3, 5}},
	}
}

type offerVariant struct {
	path  string
	value reflect.Value
}

// offerVariants returns copies of v, each differing from it in one place:
// every field changed alone, every slice and map made nil, empty, longer or
// shorter and each element or value changed alone, every pointer made nil
// and pointed at an equal copy, and a time moved, put in another location
// and in an equal location of its own. It fails on a kind it can't change,
// so a field added to an offer is covered or the test says why not.
func offerVariants(t *testing.T, path string, v reflect.Value) []offerVariant {
	t.Helper()
	typ := v.Type()
	newOf := func(x reflect.Value) reflect.Value { c := reflect.New(typ).Elem(); c.Set(x); return c }
	var out []offerVariant
	add := func(p string, x reflect.Value) { out = append(out, offerVariant{p, x}) }
	if typ == reflect.TypeOf(time.Time{}) {
		tm := v.Interface().(time.Time)
		_, offset := tm.Zone()
		name, _ := tm.Zone()
		add(path+"+1ns", reflect.ValueOf(tm.Add(time.Nanosecond)))
		add(path+" in UTC", reflect.ValueOf(tm.UTC()))
		add(path+" in an equal location of its own", reflect.ValueOf(tm.In(time.FixedZone(name, offset))))
		return out
	}
	switch typ.Kind() {
	case reflect.String:
		x := reflect.New(typ).Elem()
		x.SetString(v.String() + "x")
		add(path, x)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x := reflect.New(typ).Elem()
		x.SetInt(v.Int() + 1)
		add(path, x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		x := reflect.New(typ).Elem()
		x.SetUint(v.Uint() + 1)
		add(path, x)
	case reflect.Bool:
		x := reflect.New(typ).Elem()
		x.SetBool(!v.Bool())
		add(path, x)
	case reflect.Struct:
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				t.Fatalf("%s.%s: an unexported field the test can't change", path, f.Name)
			}
			for _, fv := range offerVariants(t, path+"."+f.Name, v.Field(i)) {
				c := newOf(v)
				c.Field(i).Set(fv.value)
				add(fv.path, c)
			}
		}
	case reflect.Pointer:
		if v.IsNil() {
			add(path+" set", reflect.New(typ.Elem()))
			break
		}
		add(path+" nil", reflect.Zero(typ))
		same := reflect.New(typ.Elem())
		same.Elem().Set(v.Elem())
		add(path+" to an equal copy", same)
		for _, ev := range offerVariants(t, path+".*", v.Elem()) {
			p := reflect.New(typ.Elem())
			p.Elem().Set(ev.value)
			add(ev.path, p)
		}
	case reflect.Slice:
		if !v.IsNil() {
			add(path+" nil", reflect.Zero(typ))
		}
		add(path+" empty", reflect.MakeSlice(typ, 0, 0))
		if v.Len() > 0 {
			add(path+" shorter", reflect.AppendSlice(reflect.MakeSlice(typ, 0, v.Len()), v.Slice(0, v.Len()-1)))
			longer := reflect.AppendSlice(reflect.MakeSlice(typ, 0, v.Len()+1), v)
			add(path+" longer", reflect.Append(longer, v.Index(v.Len()-1)))
		}
		for i := range v.Len() {
			for _, ev := range offerVariants(t, fmt.Sprintf("%s[%d]", path, i), v.Index(i)) {
				c := reflect.AppendSlice(reflect.MakeSlice(typ, 0, v.Len()), v)
				c.Index(i).Set(ev.value)
				add(ev.path, c)
			}
		}
	case reflect.Map:
		copyMap := func() reflect.Value {
			c := reflect.MakeMapWithSize(typ, v.Len())
			for _, k := range v.MapKeys() {
				c.SetMapIndex(k, v.MapIndex(k))
			}
			return c
		}
		if !v.IsNil() {
			add(path+" nil", reflect.Zero(typ))
		}
		add(path+" empty", reflect.MakeMap(typ))
		for _, k := range v.MapKeys() {
			c := copyMap()
			c.SetMapIndex(k, reflect.Value{})
			add(fmt.Sprintf("%s[%v] deleted", path, k), c)
			for _, kv := range offerVariants(t, fmt.Sprintf("%s key %v", path, k), k) {
				c := copyMap()
				c.SetMapIndex(kv.value, v.MapIndex(k))
				add(kv.path+" added", c)
			}
			for _, ev := range offerVariants(t, fmt.Sprintf("%s[%v]", path, k), v.MapIndex(k)) {
				c := copyMap()
				c.SetMapIndex(k, ev.value)
				add(ev.path, c)
			}
		}
	default:
		t.Fatalf("%s: a %s the test can't change", path, typ.Kind())
	}
	return out
}

// TestSameOfferMeaningIsDeepEqual checks that sameOfferMeaning answers as
// reflect.DeepEqual less the chain's addresses does, on every pair among an
// offer and its variants that each differ from it in one place: a field,
// a slice's or map's nil, emptiness, length or element, a pointer's target,
// a time's instant or location. So a nil slice or map meets an empty one, and
// an equal location of a time's own meets the time's.
func TestSameOfferMeaningIsDeepEqual(t *testing.T) {
	t.Parallel()
	base := fullOffer()
	offers := []offerVariant{{"the offer", reflect.ValueOf(base)}}
	offers = append(offers, offerVariants(t, "offer", reflect.ValueOf(base))...)
	differs, ignored := map[string]bool{}, 0
	for _, v := range offers[1:] {
		if !offerMeaningOracle(base, v.value.Interface().(PersistedPartOffer)) {
			differs[v.path] = true
		} else {
			ignored++
		}
	}
	// Every field but the chain's addresses changes the meaning somewhere.
	require.Greater(t, len(differs), 60)
	require.Positive(t, ignored, "no variant DeepEqual counts as the same: the addresses, a pointer to an equal copy, an equal location")
	for i, a := range offers {
		x := a.value.Interface().(PersistedPartOffer)
		for _, b := range offers[i:] {
			y := b.value.Interface().(PersistedPartOffer)
			want := offerMeaningOracle(x, y)
			require.Equal(t, want, sameOfferMeaning(x, y), "%s against %s", a.path, b.path)
			require.Equal(t, want, sameOfferMeaning(y, x), "%s against %s", b.path, a.path)
		}
	}
}

// TestSameOfferMeaningAllocatesNothing checks that comparing a kept row's
// offer with its entry's costs no allocation: a merge makes the comparison
// for every kept row under the graph lock.
func TestSameOfferMeaningAllocatesNothing(t *testing.T) {
	a, b := fullOffer(), fullOffer()
	// The entry's offer and the kept row's share a time's location, as two
	// decodes of one offer in UTC do; the addresses differ by a renewal.
	b.Chain.Layers[0].CreatedAt = a.Chain.Layers[0].CreatedAt
	b.Chain.Addresses = map[digest.Digest]BlobAddress{}
	require.True(t, sameOfferMeaning(a, b))
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if !sameOfferMeaning(a, b) {
			panic("offers differ")
		}
	}))
}

func BenchmarkSameOfferMeaning(b *testing.B) {
	x, y := fullOffer(), fullOffer()
	y.Chain.Layers[0].CreatedAt = x.Chain.Layers[0].CreatedAt
	b.ReportAllocs()
	for b.Loop() {
		if !sameOfferMeaning(x, y) {
			b.Fatal("offers differ")
		}
	}
}
