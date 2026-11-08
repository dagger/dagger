package dagql

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// forEachIndexParallel calls fn for the indexes [0, n) on up to GOMAXPROCS
// goroutines. Indexes start in increasing order, and no index starts after a
// call fails, so every index below the lowest failed one has run. It returns
// the error of the lowest failed index. Calls run concurrently: fn may write
// only state that belongs to its index.
func forEachIndexParallel(n int, fn func(i int) error) error {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for i := range n {
			if err := fn(i); err != nil {
				return err
			}
		}
		return nil
	}
	var (
		next   atomic.Int64
		failed atomic.Bool
		mu     sync.Mutex
		errIdx = n
		retErr error
		wg     sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := fn(i); err != nil {
					failed.Store(true)
					mu.Lock()
					if i < errIdx {
						errIdx, retErr = i, err
					}
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return retErr
}
