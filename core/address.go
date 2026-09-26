package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Address struct {
	Value          string
	ExternalOnly   bool
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

var _ dagql.PersistedObject = (*Address)(nil)
var _ dagql.PersistedObjectDecoder = (*Address)(nil)
var _ dagql.HasDependencyResults = (*Address)(nil)

type persistedAddressPayload struct {
	Value                  string
	ExternalOnly           bool
	BoundWorkspaceResultID uint64 `json:"boundWorkspaceResultID,omitempty"`
}

func (*Address) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Address",
		NonNull:   true,
	}
}

func (*Address) TypeDescription() string {
	return `A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.`
}

func (addr *Address) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if addr == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted address: nil address")
	}
	payload := persistedAddressPayload{Value: addr.Value, ExternalOnly: addr.ExternalOnly}
	if addr.BoundWorkspace.Self() != nil {
		wsID, err := encodePersistedObjectRef(enc, addr.BoundWorkspace, "address workspace")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.BoundWorkspaceResultID = wsID
	}
	return encodePersistedObjectPayload(payload)
}

func (*Address) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedAddressPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted address payload: %w", err)
	}
	addr := &Address{Value: persisted.Value, ExternalOnly: persisted.ExternalOnly}
	if persisted.BoundWorkspaceResultID != 0 {
		ws, err := loadPersistedObjectResultByResultID[*Workspace](ctx, dec, persisted.BoundWorkspaceResultID, "address workspace")
		if err != nil {
			return nil, err
		}
		addr.BoundWorkspace = ws
	}
	return addr, nil
}

func (addr *Address) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if addr == nil || addr.BoundWorkspace.Self() == nil {
		return nil, nil
	}
	attached, err := attach(addr.BoundWorkspace)
	if err != nil {
		return nil, fmt.Errorf("attach address workspace: %w", err)
	}
	ws, ok := attached.(dagql.ObjectResult[*Workspace])
	if !ok {
		return nil, fmt.Errorf("attach address workspace: unexpected result %T", attached)
	}
	addr.BoundWorkspace = ws
	return []dagql.AnyResult{ws}, nil
}

// resolveUserAddress applies workspace resolution to settings and interactive inputs.
// Old schema views retain their original Address behavior.
func resolveUserAddress(ctx context.Context, srv *dagql.Server, value string) (dagql.ObjectResult[*Address], error) {
	var addr dagql.ObjectResult[*Address]
	srv = srv.Canonical()
	selector := dagql.Selector{Field: "address", View: srv.View, Args: []dagql.NamedInput{{Name: "value", Value: dagql.String(value)}}}
	workspaceType, ok := srv.ObjectType("Workspace")
	if ok {
		_, ok = workspaceType.FieldSpec("resolve", srv.View)
	}
	if !ok {
		err := srv.Select(ctx, srv.Root(), &addr, selector)
		return addr, err
	}
	ws, bound := WorkspaceFromContext(ctx)
	if !bound {
		if err := srv.Select(ctx, srv.Root(), &ws, dagql.Selector{Field: "currentWorkspace"}); err != nil {
			if errors.Is(err, ErrNoCurrentWorkspace) {
				err = srv.Select(ctx, srv.Root(), &addr, selector)
			}
			return addr, err
		}
	}
	selector.Field = "resolve"
	err := srv.Select(ctx, ws, &addr, selector)
	return addr, err
}
