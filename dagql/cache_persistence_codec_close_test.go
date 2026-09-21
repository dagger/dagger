package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// persistChildLoadObj decodes by loading one child row through the decode
// context. Its decoder can be paused so a close can start while the owner's
// admitted decode is still in progress.
type persistChildLoadObj struct {
	Child AnyResult
}

type persistedChildLoadObj struct {
	ChildID uint64 `json:"childID"`
}

var persistChildLoadHooks sync.Map

type persistChildLoadHook struct {
	entered chan struct{}
	allow   chan struct{}
	// external, when true, loads the child through the public loader instead
	// of the decode context: the deliberate control for the boundary.
	external bool
}

func (*persistChildLoadObj) Type() *ast.Type {
	return &ast.Type{NamedType: "PersistChildLoadObj", NonNull: true}
}

func (obj *persistChildLoadObj) EncodePersistedObject(_ context.Context, enc *PersistEncodeContext) (PersistedObjectEncoding, error) {
	childID, err := enc.ResultRef(obj.Child)
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	payload, err := json.Marshal(persistedChildLoadObj{ChildID: childID})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{JSON: payload}, nil
}

func (*persistChildLoadObj) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, payload json.RawMessage) (Typed, error) {
	var persisted persistedChildLoadObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}
	var hook *persistChildLoadHook
	if hookAny, ok := persistChildLoadHooks.Load(dec.ResultID()); ok {
		hook = hookAny.(*persistChildLoadHook)
		close(hook.entered)
		select {
		case <-hook.allow:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("decode was never released")
		}
	}
	if hook != nil && hook.external {
		cache, err := EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		child, err := cache.LoadResultByResultID(ctx, "", dec.Server(), persisted.ChildID)
		if err != nil {
			return nil, err
		}
		return &persistChildLoadObj{Child: child}, nil
	}
	child, err := dec.ResultRef(ctx, persisted.ChildID)
	if err != nil {
		return nil, err
	}
	return &persistChildLoadObj{Child: child}, nil
}

func (obj *persistChildLoadObj) AttachDependencyResults(_ context.Context, _ AnyResult, attach func(AnyResult) (AnyResult, error)) ([]AnyResult, error) {
	if obj == nil || obj.Child == nil {
		return nil, nil
	}
	attached, err := attach(obj.Child)
	if err != nil {
		return nil, err
	}
	obj.Child = attached
	return []AnyResult{attached}, nil
}

type persistChildLoadVisitor struct{}

func (persistChildLoadVisitor) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	var persisted persistedChildLoadObj
	if err := json.Unmarshal(v.Payload, &persisted); err != nil {
		return nil, err
	}
	changed, err := VisitPersistedRow(visit, PersistedRefChild, v.Path.Field("childID"), &persisted.ChildID)
	if err != nil || !changed {
		return v.Payload, err
	}
	return json.Marshal(persisted)
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.PersistChildLoadObj", Typed: (*persistChildLoadObj)(nil), Visitor: persistChildLoadVisitor{}})
}

// TestPersistDecodeChildLoadBorrowsAdmittedOperation checks the decode
// context's boundary: a child load inside an already-admitted decode completes
// even after a close has started, and the close waits for it; a load that
// begins externally after the close starts is rejected. The deliberate control
// routes the same child load through the public loader, which begins a new
// operation and is rejected.
func TestPersistDecodeChildLoadBorrowsAdmittedOperation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		external bool
	}{
		{"decode context child load completes", false},
		{"public loader child load is rejected (control)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			ctx, cache, srv := persistedListTestCache(t, path)
			srv.InstallObject(NewClass(srv, ClassOpts[*persistChildLoadObj]{}))
			child := persistedListTestResult(t, ctx, cache, srv, "child-int", Int(41))
			childID, err := cache.PersistedResultID(child)
			require.NoError(t, err)
			parentFrame := persistCodecFrame("parent-obj", &persistChildLoadObj{})
			parentAny, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: parentFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
				return NewObjectResultForCall(&persistChildLoadObj{Child: child}, srv, parentFrame)
			})
			require.NoError(t, err)
			parentID, err := cache.PersistedResultID(parentAny)
			require.NoError(t, err)
			require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
			require.NoError(t, cache.Close(ctx))

			ctx, cache, srv = persistedListTestCache(t, path)
			srv.InstallObject(NewClass(srv, ClassOpts[*persistChildLoadObj]{}))
			hook := &persistChildLoadHook{entered: make(chan struct{}), allow: make(chan struct{}), external: tc.external}
			persistChildLoadHooks.Store(parentID, hook)
			defer persistChildLoadHooks.Delete(parentID)

			type loadResult struct {
				res AnyResult
				err error
			}
			loaded := make(chan loadResult, 1)
			go func() {
				res, err := cache.LoadResultByResultID(ctx, "test-session", srv, parentID)
				loaded <- loadResult{res: res, err: err}
			}()
			select {
			case <-hook.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("decode never entered")
			}

			closed := make(chan error, 1)
			go func() { closed <- cache.Close(context.Background()) }()
			require.Eventually(t, func() bool { return cache.closing.Load() }, 5*time.Second, time.Millisecond)
			select {
			case err := <-closed:
				t.Fatalf("close finished while an admitted decode was still running: %v", err)
			case <-time.After(50 * time.Millisecond):
			}

			// A load that begins after the close started is rejected.
			_, err = cache.LoadResultByResultID(ctx, "", srv, childID)
			require.ErrorIs(t, err, ErrCacheClosed)

			close(hook.allow)
			result := <-loaded
			require.NoError(t, <-closed)
			if tc.external {
				require.ErrorIs(t, result.err, ErrCacheClosed, "a public child load begins a new operation and is refused by the close")
				return
			}
			require.NoError(t, result.err, "the decode context borrows the admitted decode operation")
			obj := result.res.Unwrap().(*persistChildLoadObj)
			require.Equal(t, Typed(Int(41)), obj.Child.Unwrap())
			require.Equal(t, childID, uint64(obj.Child.cacheSharedResult().id))
		})
	}
}
