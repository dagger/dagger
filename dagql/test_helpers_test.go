package dagql

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

func cacheTestIntCall(field string, extras ...call.ExtraDigest) *ResultCall {
	return &ResultCall{
		Kind:         ResultCallKindField,
		Type:         NewResultCallType(Int(0).Type()),
		Field:        field,
		ExtraDigests: slices.Clone(extras),
	}
}

func cacheTestCallDigest(frame *ResultCall) digest.Digest {
	if frame == nil {
		return ""
	}
	dig, err := frame.deriveRecipeDigest(nil)
	if err != nil {
		panic(err)
	}
	return dig
}

func cacheTestIntResult(frame *ResultCall, v int) AnyResult {
	res, err := NewResultForCall(NewInt(v), frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestDetachedResult[T Typed](frame *ResultCall, self T) Result[T] {
	res, err := NewResultForCall(self, frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestPlainResult[T Typed](self T) Result[T] {
	return Result[T]{
		shared: &sharedResult{
			self:     self,
			hasValue: true,
		},
	}
}

func cacheTestIntResultWithOnRelease(frame *ResultCall, v int, onRelease func(context.Context) error) AnyResult {
	res, err := NewResultForCall(cacheTestOnReleaseInt{
		Int:       NewInt(v),
		onRelease: onRelease,
	}, frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestSizedIntResult(
	frame *ResultCall,
	value int,
	sizeBytes int64,
	usageIdentity string,
	sizeCalls *atomic.Int32,
) AnyResult {
	res, err := NewResultForCall(cacheTestSizedInt{
		Int:             NewInt(value),
		sizeByIdentity:  map[string]int64{usageIdentity: sizeBytes},
		usageIdentities: []string{usageIdentity},
		sizeCalls:       sizeCalls,
	}, frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestMutableSizedIntResult(
	frame *ResultCall,
	value int,
	sizeSource *atomic.Int64,
	usageIdentity string,
	sizeCalls *atomic.Int32,
) AnyResult {
	res, err := NewResultForCall(cacheTestSizedInt{
		Int:                  NewInt(value),
		sizeSourceByIdentity: map[string]*atomic.Int64{usageIdentity: sizeSource},
		usageIdentities:      []string{usageIdentity},
		sizeCalls:            sizeCalls,
		sizeMayChange:        true,
	}, frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestDetachedObjectResult[T Typed](frame *ResultCall, srv *Server, self T) ObjectResult[T] {
	res, err := NewObjectResultForCall(self, srv, frame)
	if err != nil {
		panic(err)
	}
	return res
}

func cacheTestMustID(t testing.TB, idable IDable) *call.ID {
	t.Helper()
	id, err := idable.ID()
	if err != nil {
		t.Fatalf("ID(): %v", err)
	}
	return id
}

func cacheTestMustRecipeID(t testing.TB, ctx context.Context, idable RecipeIDable) *call.ID {
	t.Helper()
	id, err := idable.RecipeID(ctx)
	if err != nil {
		t.Fatalf("RecipeID(): %v", err)
	}
	return id
}

func cacheTestMustEncodeID(t testing.TB, idable IDable) string {
	t.Helper()
	enc, err := cacheTestMustID(t, idable).Encode()
	if err != nil {
		t.Fatalf("Encode(): %v", err)
	}
	return enc
}
