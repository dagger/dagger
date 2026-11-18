package core

import (
	"container/list"
	"context"
	"errors"
	"slices"
	"sync"
)

// maxConvertedReceiversWeight bounds the receiver JSON, in bytes, that a
// session keeps converted (see convertedReceivers).
const maxConvertedReceiversWeight = 32 << 20

// maxLargeConvertedReceivers bounds the large receivers, more than a quarter
// of the bound on their own, that a session keeps converted.
const maxLargeConvertedReceivers = 4

// convertedReceiverOverhead is what a kept conversion costs beside its JSON:
// its entry, channel, list element and map slot.
const convertedReceiverOverhead = 512

// convertedReceivers holds a session's most recently used receiver
// conversions. Calls that convert the same receiver at once share one
// conversion. Once the conversions kept weigh more than maxWeight bytes, the
// least recently used are dropped: an object never changes in place, so every
// call on an evolving object has a new receiver, and keeping every conversion
// would keep a JSON copy of every state the session's objects went through,
// beside the results the session holds. The next call on a dropped receiver
// converts it again.
//
// A large receiver, weighing more than a quarter of maxWeight, such as a
// large collection whose items are all being called, is kept apart among the
// maxLargeConvertedReceivers most recently used large receivers, so that a
// few of them used in turn neither empty the LRU nor evict each other.
type convertedReceivers struct {
	maxWeight int

	mu      sync.Mutex
	entries map[convertedReceiverKey]*list.Element
	// lru holds the kept *convertedReceiver values, most recently used first.
	lru    *list.List
	weight int
	// large holds the receivers too large for the LRU, most recently used
	// first.
	large []*convertedReceiver
}

// convertedReceiverKey identifies a receiver conversion by the receiver's
// result and the result scope it was converted in.
type convertedReceiverKey struct {
	resultID uint64
	scope    string
}

type convertedReceiver struct {
	key convertedReceiverKey
	// elem is the receiver's element of the LRU while it is kept there, and
	// weight what it counts toward convertedReceivers.weight; both guarded by
	// convertedReceivers.mu.
	elem   *list.Element
	weight int
	// joined counts the calls that found the conversion kept; guarded by
	// convertedReceivers.mu.
	joined int
	// done is closed once json and err are set.
	done chan struct{}
	json []byte
	err  error
}

func newConvertedReceivers(maxWeight int) *convertedReceivers {
	return &convertedReceivers{maxWeight: maxWeight, entries: map[convertedReceiverKey]*list.Element{}, lru: list.New()}
}

// get returns the receiver JSON for key, converting it with convert unless a
// conversion is kept or in flight.
func (r *convertedReceivers) get(ctx context.Context, key convertedReceiverKey, convert func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		r.mu.Lock()
		entry := r.lookupLocked(key)
		if entry == nil {
			entry = &convertedReceiver{key: key, done: make(chan struct{})}
			// In flight, it weighs its overhead; it is weighed again once
			// converted.
			r.keepLocked(entry, convertedReceiverOverhead)
			r.mu.Unlock()
			return r.convert(ctx, entry, convert)
		}
		entry.joined++
		r.mu.Unlock()
		select {
		case <-entry.done:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		if entry.err == nil {
			return entry.json, nil
		}
		// A conversion that ended with its own caller's cancellation says
		// nothing about this call, which converts again.
		if ctx.Err() == nil && (errors.Is(entry.err, context.Canceled) || errors.Is(entry.err, context.DeadlineExceeded)) {
			continue
		}
		return nil, entry.err
	}
}

// convert runs entry's conversion and publishes its outcome to the calls
// waiting on it.
func (r *convertedReceivers) convert(ctx context.Context, entry *convertedReceiver, convert func(context.Context) ([]byte, error)) ([]byte, error) {
	defer func() {
		if p := recover(); p != nil {
			entry.err = errors.New("convert receiver: panic")
			r.mu.Lock()
			r.removeLocked(entry)
			r.mu.Unlock()
			close(entry.done)
			panic(p)
		}
	}()
	entry.json, entry.err = convert(ctx)
	r.mu.Lock()
	r.removeLocked(entry)
	// Keep it at its size, unless it was dropped meanwhile and another call
	// is converting the same receiver again.
	if entry.err == nil && r.lookupLocked(entry.key) == nil {
		r.keepLocked(entry, convertedReceiverOverhead+len(entry.json))
	}
	r.mu.Unlock()
	close(entry.done)
	return entry.json, entry.err
}

// lookupLocked returns the kept entry for key, marking it most recently used.
func (r *convertedReceivers) lookupLocked(key convertedReceiverKey) *convertedReceiver {
	if elem, ok := r.entries[key]; ok {
		r.lru.MoveToFront(elem)
		return elem.Value.(*convertedReceiver)
	}
	for i, entry := range r.large {
		if entry.key == key {
			copy(r.large[1:i+1], r.large[:i])
			r.large[0] = entry
			return entry
		}
	}
	return nil
}

// keepLocked keeps entry, weighing weight, as the most recently used,
// dropping the least recently used entries that no longer fit.
func (r *convertedReceivers) keepLocked(entry *convertedReceiver, weight int) {
	if weight > r.maxWeight/4 {
		if len(r.large) >= maxLargeConvertedReceivers {
			clear(r.large[maxLargeConvertedReceivers-1:])
			r.large = r.large[:maxLargeConvertedReceivers-1]
		}
		r.large = slices.Insert(r.large, 0, entry)
		return
	}
	entry.elem = r.lru.PushFront(entry)
	entry.weight = weight
	r.entries[entry.key] = entry.elem
	r.weight += weight
	for r.weight > r.maxWeight {
		r.removeLocked(r.lru.Back().Value.(*convertedReceiver))
	}
}

// removeLocked forgets entry, if it is kept.
func (r *convertedReceivers) removeLocked(entry *convertedReceiver) {
	if i := slices.Index(r.large, entry); i >= 0 {
		r.large = slices.Delete(r.large, i, i+1)
		return
	}
	if entry.elem == nil {
		return
	}
	if r.entries[entry.key] == entry.elem {
		delete(r.entries, entry.key)
	}
	r.lru.Remove(entry.elem)
	entry.elem = nil
	r.weight -= entry.weight
}
