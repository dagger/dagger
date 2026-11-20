package dagql

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestMain has every merge of the package's tests decided by both the
// narrowed cycle search and the whole one it replaced, and fails the run if
// any merge's two decisions differ. It also checks every blob-backed cache's
// kept measure of its pool against a walk as the cache closes
// (VerifyPoolMeasure), and fails the run if any differs.
func TestMain(m *testing.M) {
	var (
		mu            sync.Mutex
		judged        int
		disagreements []string
		pools         int
		poolDrifts    []string
		poolChecked   time.Duration
	)
	testCacheClosing = func(c *Cache) {
		if !c.blobBacked {
			return
		}
		at := time.Now()
		err := c.VerifyPoolMeasure()
		took := time.Since(at)
		mu.Lock()
		defer mu.Unlock()
		pools++
		poolChecked += took
		if err != nil {
			poolDrifts = append(poolDrifts, err.Error())
		}
	}
	JudgeMergeGraphs(func(_ context.Context, j MergeGraphJudgement) {
		mu.Lock()
		defer mu.Unlock()
		judged++
		if j.Disagrees() {
			disagreements = append(disagreements, fmt.Sprintf("merge from %s: narrowed %v, whole %v", j.From, j.Narrow, j.Whole))
		}
	})
	code := m.Run()
	mu.Lock()
	if len(disagreements) > 0 {
		fmt.Fprintf(os.Stderr, "the narrowed and whole cycle searches disagreed on %d of %d merges:\n", len(disagreements), judged)
		for _, d := range disagreements {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "merge graph judge: %d merges, no disagreement\n", judged)
	}
	if len(poolDrifts) > 0 {
		fmt.Fprintf(os.Stderr, "the kept pool measure differed from a walk in %d of %d blob-backed caches, checked in %s:\n", len(poolDrifts), pools, poolChecked)
		for _, d := range poolDrifts {
			fmt.Fprintln(os.Stderr, "  "+d)
		}
		code = 1
	} else {
		fmt.Fprintf(os.Stderr, "kept pool measure: %d blob-backed caches checked in %s, none differed\n", pools, poolChecked)
	}
	mu.Unlock()
	os.Exit(code)
}
