package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestConvertedReceiversKeepRecentlyUsed(t *testing.T) {
	t.Parallel()

	var conversions atomic.Int32
	receiverJSON := func(name string, size int) func(context.Context) ([]byte, error) {
		return func(context.Context) ([]byte, error) {
			conversions.Add(1)
			return []byte(name + strings.Repeat(" ", size-len(name))), nil
		}
	}
	// Room for two 100-byte receivers.
	receivers := newConvertedReceivers(250)
	get := func(id uint64, scope string, size int) []byte {
		got, err := receivers.get(t.Context(), convertedReceiverKey{resultID: id, scope: scope}, receiverJSON(fmt.Sprint(id, scope), size))
		require.NoError(t, err)
		return got
	}
	a := get(1, "", 100)
	require.Same(t, &a[0], &get(1, "", 100)[0], "a receiver is converted once")
	require.EqualValues(t, 1, conversions.Load())
	get(1, "view", 100)
	require.EqualValues(t, 2, conversions.Load(), "each scope converts the receiver itself")
	get(1, "", 100)
	get(2, "", 100)
	require.EqualValues(t, 3, conversions.Load())
	require.Same(t, &a[0], &get(1, "", 100)[0], "a recently used conversion is kept")
	get(1, "view", 100)
	require.EqualValues(t, 4, conversions.Load(), "the least recently used conversion was dropped")

	big := get(3, "", 1000)
	require.Same(t, &big[0], &get(3, "", 1000)[0], "a receiver beyond the bound is kept apart")
	require.Same(t, &a[0], &get(1, "", 100)[0], "a receiver beyond the bound drops nothing")
	for id := range uint64(maxLargeConvertedReceivers) {
		get(10+id, "", 1000)
	}
	before := conversions.Load()
	get(3, "", 1000)
	require.Equal(t, before+1, conversions.Load(), "only the most recently used receivers beyond the bound are kept")

	failure := errors.New("convert failed")
	_, err := receivers.get(t.Context(), convertedReceiverKey{resultID: 4}, func(context.Context) ([]byte, error) { return nil, failure })
	require.ErrorIs(t, err, failure)
	got, err := receivers.get(t.Context(), convertedReceiverKey{resultID: 4}, receiverJSON("4", 10))
	require.NoError(t, err, "a failed conversion is not kept")
	require.Equal(t, "4         ", string(got))

	receivers.mu.Lock()
	defer receivers.mu.Unlock()
	require.LessOrEqual(t, receivers.weight, receivers.maxWeight)
	weight := 0
	for e := receivers.lru.Front(); e != nil; e = e.Next() {
		weight += e.Value.(*convertedReceiver).weight
	}
	require.Equal(t, receivers.weight, weight)
	require.Len(t, receivers.entries, receivers.lru.Len())
}

func TestConvertedReceiversShareConversions(t *testing.T) {
	t.Parallel()

	receivers := newConvertedReceivers(maxConvertedReceiversWeight)
	release := make(chan struct{})
	var conversions atomic.Int32
	convert := func(context.Context) ([]byte, error) {
		conversions.Add(1)
		<-release
		return []byte(`{}`), nil
	}
	var group errgroup.Group
	for range 8 {
		group.Go(func() error {
			_, err := receivers.get(t.Context(), convertedReceiverKey{resultID: 1}, convert)
			return err
		})
	}
	time.AfterFunc(50*time.Millisecond, func() { close(release) })
	require.NoError(t, group.Wait())
	require.EqualValues(t, 1, conversions.Load(), "calls at once share one conversion")
}

// A call waiting on a conversion whose own caller gave up converts again
// rather than failing with that caller's cancellation.
func TestConvertedReceiversWaiterOutlivesCanceledConversion(t *testing.T) {
	t.Parallel()

	receivers := newConvertedReceivers(maxConvertedReceiversWeight)
	key := convertedReceiverKey{resultID: 1}
	ownerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	ownerErr := make(chan error, 1)
	go func() {
		_, err := receivers.get(ownerCtx, key, func(ctx context.Context) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		ownerErr <- err
	}()
	<-started
	waiter := make(chan []byte, 1)
	go func() {
		got, err := receivers.get(t.Context(), key, func(context.Context) ([]byte, error) { return []byte("waiter"), nil })
		if err != nil {
			got = []byte(err.Error())
		}
		waiter <- got
	}()
	// Let the waiter join the conversion in flight before it fails.
	require.Eventually(t, func() bool {
		receivers.mu.Lock()
		defer receivers.mu.Unlock()
		elem, ok := receivers.entries[key]
		return ok && elem.Value.(*convertedReceiver).joined == 1
	}, 10*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-ownerErr, context.Canceled)
	require.Equal(t, "waiter", string(<-waiter))
}
