package cond

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCondInitialWaitBlocks(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	c := NewStatefulCond(&mu)

	waited := make(chan struct{})

	mu.Lock()

	go func() {
		c.Wait()
		close(waited)
	}()

	select {
	case <-time.After(50 * time.Millisecond):
	case <-waited:
		require.Fail(t, "wait should have blocked")
	}

	c.Signal()

	select {
	case <-time.After(300 * time.Millisecond):
		require.Fail(t, "wait should have resumed")
	case <-waited:
	}

	mu.Unlock()
}

func TestInitialSignalDoesntBlock(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	c := NewStatefulCond(&mu)

	waited := make(chan struct{})

	c.Signal()

	mu.Lock()

	go func() {
		c.Wait()
		close(waited)
	}()

	select {
	case <-time.After(300 * time.Millisecond):
		require.Fail(t, "wait should have resumed")
	case <-waited:
	}

	waited = make(chan struct{})
	go func() {
		c.Wait()
		close(waited)
	}()

	select {
	case <-time.After(50 * time.Millisecond):
	case <-waited:
		require.Fail(t, "wait should have blocked")
	}

	c.Signal()

	<-waited

	mu.Unlock()
}

func TestSignalBetweenWaits(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	c := NewStatefulCond(&mu)

	mu.Lock()

	waited := make(chan struct{})

	go func() {
		c.Wait()
		close(waited)
	}()

	select {
	case <-time.After(50 * time.Millisecond):
	case <-waited:
		require.Fail(t, "wait should have blocked")
	}

	c.Signal()

	<-waited

	c.Signal()

	waited = make(chan struct{})
	go func() {
		c.Wait()
		close(waited)
	}()

	<-waited

	mu.Unlock()
}
