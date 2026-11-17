package dagql

import (
	"context"
	"fmt"
	"slices"
)

type payloadInputIDs []uint64

func newPayloadInputIDs(ids []uint64) *payloadInputIDs {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	inputs := payloadInputIDs(slices.Compact(ids))
	return &inputs
}

// ResultPayloadInputIDs reads the exact row's declared immutable input
// references without decoding it or waiting for evaluation. The returned
// IDs are positive evidence only. Other payload references may exist.
func (c *Cache) ResultPayloadInputIDs(ctx context.Context, sessionID string, resultID uint64) ([]uint64, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, err
	}
	defer op.finish(false)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	row := c.resultsByID[sharedResultID(resultID)]
	if row == nil || row.noValueLocked() || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
		return nil, fmt.Errorf("payload inputs: result %d is unavailable to the session", resultID)
	}
	if row.attachmentState() != resultAttachmentClean {
		return nil, fmt.Errorf("%w: payload inputs for result %d", ErrPersistStateNotReady, resultID)
	}

	// An alias owns its value row. Its call frame remains descriptive.
	row.payloadMu.RLock()
	var source sharedResultID
	if row.valueSource != nil {
		source = row.valueSource.id
	} else if row.persistedEnvelope != nil && row.persistedEnvelope.Kind == persistedResultKindAlias {
		source = sharedResultID(row.persistedEnvelope.ValueResultID)
	}
	row.payloadMu.RUnlock()
	if source != 0 {
		if _, owned := row.deps[source]; owned && source != row.id {
			return []uint64{uint64(source)}, nil
		}
		return nil, fmt.Errorf("payload inputs: alias %d has no owned value row", resultID)
	}
	if row.payloadInputs == nil {
		return nil, nil
	}
	for _, id := range *row.payloadInputs {
		if _, owned := row.deps[sharedResultID(id)]; !owned {
			return nil, fmt.Errorf("payload inputs: result %d no longer owns input %d", resultID, id)
		}
	}
	return slices.Clone(*row.payloadInputs), nil
}
