package dagql

import (
	"context"
	"fmt"
)

// StoredSizesAloneMax is how many roots StoredSizesAlone answers for at
// once.
const StoredSizesAloneMax = 64

// StoredSizesAlone answers, for each root, what StoredSizes answers for that
// root alone, StoredSizes(ctx, []uint64{root}, nowUnix, held)[0]: what a
// bundle of the root and nothing else carries, judging held's holdings at
// nowUnix, for at most StoredSizesAloneMax roots. It asks StoredSizes root by
// root, one walk of each root's closure.
func (c *Cache) StoredSizesAlone(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) ([]StoredSize, error) {
	if len(roots) > StoredSizesAloneMax {
		return nil, fmt.Errorf("stored sizes alone: %d roots, more than %d", len(roots), StoredSizesAloneMax)
	}
	sizes := make([]StoredSize, len(roots))
	for i, root := range roots {
		own, err := c.StoredSizes(ctx, []uint64{root}, nowUnix, held)
		if err != nil {
			return nil, err
		}
		sizes[i] = own[0]
	}
	return sizes, nil
}
