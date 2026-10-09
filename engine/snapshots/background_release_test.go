package snapshots

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A release runs after run returns; at most max run at a time, and run waits
// for a slot beyond that.
func TestBackgroundReleasesBounded(t *testing.T) {
	t.Parallel()
	b := newBackgroundReleases(2)
	started := make(chan int, 3)
	finish := make(chan struct{})
	for i := range 2 {
		b.run(func() error {
			started <- i
			<-finish
			return nil
		})
	}
	<-started
	<-started

	third := make(chan struct{})
	go func() {
		b.run(func() error {
			started <- 2
			return nil
		})
		close(third)
	}()
	select {
	case <-third:
		t.Fatal("a third release started while two were pending")
	case <-time.After(100 * time.Millisecond):
	}

	close(finish)
	<-third
	require.Equal(t, 2, <-started)
	b.wait()
}

// wait waits for the releases in flight; later releases run before run
// returns. Release errors are logged, not returned.
func TestBackgroundReleasesWaitAndErrors(t *testing.T) {
	t.Parallel()
	b := newBackgroundReleases(maxPendingReleases)
	var mu sync.Mutex
	var logged []error
	b.logError = func(err error) {
		if err != nil {
			mu.Lock()
			logged = append(logged, err)
			mu.Unlock()
		}
	}
	injected := errors.New("injected unmount failure")
	finish := make(chan struct{})
	done := false
	b.run(func() error {
		<-finish
		done = true
		return injected
	})

	waited := make(chan struct{})
	go func() {
		b.wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("wait returned with a release in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(finish)
	<-waited
	require.True(t, done)

	after := false
	b.run(func() error {
		after = true
		return nil
	})
	require.True(t, after, "a release after wait runs before run returns")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []error{injected}, logged)
}
