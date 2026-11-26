package dagql

import (
	"fmt"
	"runtime"
	"sync"
	"weak"
)

// sharedValues holds one copy of each distinct value its holders share, by
// key, weakly: a copy lives while a holder points to it, and once none does
// the collector frees it and the table forgets its key. So the table holds
// only the values in use, however many come and go over a process's life.
// The table is its own allocation, apart from the values: the runtime keeps
// a value's cleanup, and so the table, alive while the value lives.
//
// Nothing changes a shared value: a holder that would change it makes a copy
// of its own. Tests check that (verifySharedValues).
type sharedValues[K comparable, V any] struct {
	mu    sync.Mutex
	byKey map[K]weak.Pointer[V]
	// keyOf is the key a value is shared under, which a check of the values
	// in tests takes afresh (verifySharedValues).
	keyOf func(*V) K
}

// sharedValue is a value's key and weak pointer, which its cleanup takes.
type sharedValue[K comparable, V any] struct {
	key  K
	weak weak.Pointer[V]
}

// verifySharedValues, set only by tests, has every share of a value the table
// holds check that the value is still the one its key names, and panic if it
// isn't: a holder changed a shared value.
var verifySharedValues bool

// share returns the table's value for key, or adds the one newValue makes,
// which the table owns from then on.
func (t *sharedValues[K, V]) share(key K, newValue func() *V) *V {
	t.mu.Lock()
	defer t.mu.Unlock()
	if w, ok := t.byKey[key]; ok {
		if v := w.Value(); v != nil {
			if verifySharedValues && t.keyOf(v) != key {
				panic(fmt.Sprintf("shared value changed: shared under %v, now %v", key, t.keyOf(v)))
			}
			return v
		}
	}
	v := newValue()
	if t.byKey == nil {
		t.byKey = map[K]weak.Pointer[V]{}
	}
	w := weak.Make(v)
	t.byKey[key] = w
	runtime.AddCleanup(v, t.forget, sharedValue[K, V]{key: key, weak: w})
	return v
}

// forget drops a freed value's key, unless the key names another value since.
func (t *sharedValues[K, V]) forget(v sharedValue[K, V]) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byKey[v.key] == v.weak {
		delete(t.byKey, v.key)
	}
}

// verify checks every value the table holds against its key, and returns an
// error for the first that differs: one a holder changed.
func (t *sharedValues[K, V]) verify() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, w := range t.byKey {
		if v := w.Value(); v != nil && t.keyOf(v) != key {
			return fmt.Errorf("shared value changed: shared under %v, now %v", key, t.keyOf(v))
		}
	}
	return nil
}

// count returns how many values the table holds, freed ones not yet
// forgotten included.
func (t *sharedValues[K, V]) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.byKey)
}
