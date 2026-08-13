package dagql

import "time"

// cacheEgraphLockMode identifies whether an observed egraphMu acquisition was
// shared or exclusive. The observer is intentionally internal and opt-in: it
// exists so focused cache benchmarks can measure lock wait and hold time at the
// operation boundary without changing mutex semantics.
type cacheEgraphLockMode uint8

const (
	cacheEgraphLockRead cacheEgraphLockMode = iota + 1
	cacheEgraphLockWrite
)

type cacheEgraphLockObservation struct {
	Operation string
	Mode      cacheEgraphLockMode
	Wait      time.Duration
	Hold      time.Duration
}

// cacheEgraphMeasurementObservation reports bounded operation details that
// lock timing alone cannot recover. Count and Maximum are deliberately generic
// so the benchmark observer can record such things as candidate cardinality,
// collected results, and peak collector queue depth without exposing benchmark
// types to cache code.
type cacheEgraphMeasurementObservation struct {
	Operation string
	Duration  time.Duration
	Count     int64
	Maximum   int64
}

type cacheEgraphLockObserver interface {
	cacheEgraphLockShouldMeasure(operation string, mode cacheEgraphLockMode) bool
	cacheEgraphLockAcquired(operation string, mode cacheEgraphLockMode)
	cacheEgraphLockObserved(cacheEgraphLockObservation)
	cacheEgraphMeasurementObserved(cacheEgraphMeasurementObservation)
}

type cacheEgraphMeasurementToken struct {
	observer  cacheEgraphLockObserver
	operation string
	started   time.Time
}

type cacheEgraphLockToken struct {
	observer  cacheEgraphLockObserver
	operation string
	mode      cacheEgraphLockMode
	wait      time.Duration
	acquired  time.Time
}

func (c *Cache) lockEgraphMeasured(operation string) cacheEgraphLockToken {
	observer := c.egraphLockObserver
	if observer == nil || !observer.cacheEgraphLockShouldMeasure(operation, cacheEgraphLockWrite) {
		c.egraphMu.Lock()
		return cacheEgraphLockToken{}
	}
	started := time.Now()
	c.egraphMu.Lock()
	acquired := time.Now()
	observer.cacheEgraphLockAcquired(operation, cacheEgraphLockWrite)
	return cacheEgraphLockToken{
		observer:  observer,
		operation: operation,
		mode:      cacheEgraphLockWrite,
		wait:      acquired.Sub(started),
		acquired:  acquired,
	}
}

func (c *Cache) unlockEgraphMeasured(token cacheEgraphLockToken) {
	if token.observer == nil {
		c.egraphMu.Unlock()
		return
	}
	hold := time.Since(token.acquired)
	c.egraphMu.Unlock()
	token.observer.cacheEgraphLockObserved(cacheEgraphLockObservation{
		Operation: token.operation,
		Mode:      token.mode,
		Wait:      token.wait,
		Hold:      hold,
	})
}

func (c *Cache) rlockEgraphMeasured(operation string) cacheEgraphLockToken {
	observer := c.egraphLockObserver
	if observer == nil || !observer.cacheEgraphLockShouldMeasure(operation, cacheEgraphLockRead) {
		c.egraphMu.RLock()
		return cacheEgraphLockToken{}
	}
	started := time.Now()
	c.egraphMu.RLock()
	acquired := time.Now()
	observer.cacheEgraphLockAcquired(operation, cacheEgraphLockRead)
	return cacheEgraphLockToken{
		observer:  observer,
		operation: operation,
		mode:      cacheEgraphLockRead,
		wait:      acquired.Sub(started),
		acquired:  acquired,
	}
}

func (c *Cache) runlockEgraphMeasured(token cacheEgraphLockToken) {
	if token.observer == nil {
		c.egraphMu.RUnlock()
		return
	}
	hold := time.Since(token.acquired)
	c.egraphMu.RUnlock()
	token.observer.cacheEgraphLockObserved(cacheEgraphLockObservation{
		Operation: token.operation,
		Mode:      token.mode,
		Wait:      token.wait,
		Hold:      hold,
	})
}

func (c *Cache) beginEgraphMeasurement(operation string) cacheEgraphMeasurementToken {
	observer := c.egraphLockObserver
	if observer == nil {
		return cacheEgraphMeasurementToken{}
	}
	return cacheEgraphMeasurementToken{
		observer:  observer,
		operation: operation,
		started:   time.Now(),
	}
}

func (c *Cache) endEgraphMeasurement(token cacheEgraphMeasurementToken, count, maximum int) {
	if token.observer == nil {
		return
	}
	token.observer.cacheEgraphMeasurementObserved(cacheEgraphMeasurementObservation{
		Operation: token.operation,
		Duration:  time.Since(token.started),
		Count:     int64(count),
		Maximum:   int64(maximum),
	})
}

func (c *Cache) observeEgraphMeasurement(operation string, count, maximum int) {
	observer := c.egraphLockObserver
	if observer == nil {
		return
	}
	observer.cacheEgraphMeasurementObserved(cacheEgraphMeasurementObservation{
		Operation: operation,
		Count:     int64(count),
		Maximum:   int64(maximum),
	})
}
