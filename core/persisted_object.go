package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func encodePersistedObjectPayload(payload any) (dagql.PersistedObjectEncoding, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return dagql.PersistedObjectEncoding{JSON: raw}, nil
}

func encodePersistedObjectRawJSON(raw json.RawMessage) dagql.PersistedObjectEncoding {
	return dagql.PersistedObjectEncoding{JSON: raw}
}

// unmarshalPersistedPayload decodes one persisted payload struct, keeping
// untyped numbers exact.
func unmarshalPersistedPayload(raw json.RawMessage, dst any) error {
	return dagql.UnmarshalLosslessJSON(raw, dst)
}

func persistedDecodeQuery(dec *dagql.PersistDecodeContext) (*Query, error) {
	dag := dec.Server()
	if dag == nil {
		return nil, fmt.Errorf("persisted decode query: nil dagql server")
	}
	root := dag.Root()
	if root == nil {
		return nil, fmt.Errorf("persisted decode query: nil dagql root")
	}
	query, ok := dagql.UnwrapAs[*Query](root)
	if !ok {
		return nil, fmt.Errorf("persisted decode query: root is %T", root.Unwrap())
	}
	return query, nil
}

// encodePersistedCallID preserves a typed call ID in its tagged handle or
// recipe form through the encode context.
func encodePersistedCallID(enc *dagql.PersistEncodeContext, id *call.ID) (string, error) {
	encoded, err := enc.CallID(id)
	if err != nil {
		return "", fmt.Errorf("encode persisted call ID: %w", err)
	}
	return encoded, nil
}

// encodePersistedObjectRef records the exact attached row referenced by ref.
func encodePersistedObjectRef(enc *dagql.PersistEncodeContext, ref any, label string) (uint64, error) {
	if enc == nil {
		return 0, fmt.Errorf("encode persisted %s ref: nil encode context", label)
	}
	switch x := ref.(type) {
	case nil:
		return 0, fmt.Errorf("encode persisted %s ref: nil value", label)
	case dagql.AnyResult:
		resultID, err := enc.ResultRef(x)
		if err != nil {
			return 0, fmt.Errorf("encode persisted %s ref: %w", label, err)
		}
		return resultID, nil
	default:
		return 0, fmt.Errorf("encode persisted %s ref: unsupported value %T", label, ref)
	}
}

// decodePersistedCallID restores a typed call ID through the decode context.
func decodePersistedCallID(dec *dagql.PersistDecodeContext, raw string) (*call.ID, error) {
	id, err := dec.CallID(raw)
	if err != nil {
		return nil, fmt.Errorf("decode persisted call ID: %w", err)
	}
	return id, nil
}

// loadPersistedResultByResultID loads the exact local row named by resultID.
// Zero is explicit absence.
func loadPersistedResultByResultID(ctx context.Context, dec *dagql.PersistDecodeContext, resultID uint64, label string) (dagql.AnyResult, error) {
	if resultID == 0 {
		return nil, nil
	}
	if _, err := persistedDecodeQuery(dec); err != nil {
		return nil, fmt.Errorf("load persisted %s query: %w", label, err)
	}
	res, err := dec.ResultRef(ctx, resultID)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s result: %w", label, err)
	}
	return res, nil
}

func loadPersistedObjectResultByResultID[T dagql.Typed](ctx context.Context, dec *dagql.PersistDecodeContext, resultID uint64, label string) (dagql.ObjectResult[T], error) {
	if resultID == 0 {
		return dagql.ObjectResult[T]{}, nil
	}
	res, err := loadPersistedResultByResultID(ctx, dec, resultID, label)
	if err != nil {
		return dagql.ObjectResult[T]{}, err
	}
	obj, ok := res.(dagql.AnyObjectResult)
	if !ok {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s object: result %d is %T", label, resultID, res)
	}
	typed, ok := obj.(dagql.ObjectResult[T])
	if !ok {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s: unexpected object result %T", label, obj)
	}
	return typed, nil
}

// loadPersistedSnapshotLinkByResultID resolves one declared storage role of
// the row being decoded.
func loadPersistedSnapshotLinkByResultID(ctx context.Context, dec *dagql.PersistDecodeContext, label, role string) (dagql.PersistedSnapshotRefLink, error) {
	if dec.ResultID() == 0 {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link: zero result ID", label)
	}
	if _, err := persistedDecodeQuery(dec); err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link query: %w", label, err)
	}
	link, err := dec.SnapshotRole(ctx, role)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link: %w", label, err)
	}
	return link, nil
}

// loadPersistedSnapshotLinksByResultID resolves every declared storage role
// of the row being decoded.
func loadPersistedSnapshotLinksByResultID(ctx context.Context, dec *dagql.PersistDecodeContext, label string) ([]dagql.PersistedSnapshotRefLink, error) {
	if dec.ResultID() == 0 {
		return nil, fmt.Errorf("load persisted %s snapshot links: zero result ID", label)
	}
	if _, err := persistedDecodeQuery(dec); err != nil {
		return nil, fmt.Errorf("load persisted %s snapshot links query: %w", label, err)
	}
	links, err := dec.SnapshotRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s snapshot links: %w", label, err)
	}
	return links, nil
}
