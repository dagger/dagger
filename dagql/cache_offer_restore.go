package dagql

import (
	"context"
	"errors"
	"fmt"
)

type restoredFinalOffer struct {
	row     *sharedResult
	address PersistedPartAddress
}

// Boot validates local output shapes before opening storage. Completed local
// outputs may have an obsolete offer, whose hold ends only after link restore.
func (c *Cache) restoredFinalOffersLocked() ([]restoredFinalOffer, error) {
	var final []restoredFinalOffer
	for _, row := range c.resultsByID {
		if row.persistedEnvelope == nil {
			continue
		}
		rec := PersistedRecord{ResultID: uint64(row.id), Envelope: *row.persistedEnvelope, Call: row.loadResultCall(), SnapshotLinks: row.loadSnapshotOwnerLinks()}
		outputs, err := mapTransferredOutputs(rec)
		if err != nil {
			return nil, fmt.Errorf("restore outputs of result %d: %w", row.id, err)
		}
		parts := map[string]CapturedCodecOutput{}
		for _, part := range outputs {
			key, _ := partAddressKey(part.Address)
			parts[key] = part
		}
		for key, offer := range row.partOffers {
			part, ok := parts[key]
			if !ok {
				return nil, fmt.Errorf("restore offer on result %d: unknown part %s", row.id, key)
			}
			if err := validateTransferOffer(offer.record, part); err != nil {
				return nil, err
			}
			switch part.State {
			case "completed", "absent":
				final = append(final, restoredFinalOffer{row, part.Address})
			case "pending":
			default:
				return nil, fmt.Errorf("restore offer on result %d: invalid state %s", row.id, part.State)
			}
		}
	}
	return final, nil
}

func (c *Cache) settleRestoredOffers(ctx context.Context, final []restoredFinalOffer) error {
	ctx = context.WithoutCancel(ctx)
	c.egraphMu.Lock()
	var queue collectionQueue
	var rerr error
	for _, part := range final {
		q, err := c.retirePartOfferLocked(ctx, part.row, part.address)
		queue = append(queue, q...)
		rerr = errors.Join(rerr, err)
	}
	releases, err := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(rerr, err, runOnReleaseFuncs(ctx, releases))
}
