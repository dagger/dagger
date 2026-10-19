package dagql

import (
	"context"
	"errors"
	"testing"
)

func TestCacheUsageRechecksEmptyPayloadAtFinalization(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	row := publishProgressValue(t, ctx, c, "holder", "empty", NewInt(1), false).cacheSharedResult()
	snapshot, err := c.collectUsageMeasurementInputs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := snapshot.close(ctx); err != nil {
			t.Error(err)
		}
	}()

	c.egraphMu.Lock()
	row.payloadMu.Lock()
	row.self = &usageCallbackValue{Int: NewInt(2), callback: func(string) {
		t.Error("unsampled provider called during finalization")
	}}
	row.payloadRevision++
	row.payloadMu.Unlock()
	_, err = snapshot.finalizeLocked()
	c.egraphMu.Unlock()
	if !errors.Is(err, errCacheUsageChanged) {
		t.Fatalf("new provider was accepted without sampling: %v", err)
	}
}

func TestCacheUsageClearsMeasurementsAfterPayloadBecomesEmpty(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	row := publishUsageValue(t, c, "provider", &usageCallbackValue{
		Int: NewInt(1), callback: func(string) {},
	}).cacheSharedResult()
	if err := c.measureAllResultSizes(ctx); err != nil {
		t.Fatal(err)
	}
	if row.cacheUsageSizeByIdentity["shared-snapshot"] != 64 {
		t.Fatalf("fixture has no measured usage: %v", row.cacheUsageSizeByIdentity)
	}
	c.egraphMu.Lock()
	row.payloadMu.Lock()
	row.self = NewInt(2)
	row.payloadRevision++
	row.payloadMu.Unlock()
	c.egraphMu.Unlock()
	if err := c.measureAllResultSizes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(row.cacheUsageSizeByIdentity) != 0 {
		t.Fatalf("empty payload retained old measurements: %v", row.cacheUsageSizeByIdentity)
	}
}
