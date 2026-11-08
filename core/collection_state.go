package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

const privateCollectionStateFieldName = "__withCollectionState"

// Reconstruct an inline collection from its attached original base and stored
// fields. Unlike fabricated private-field calls, this operation can be replayed.
// StateValue retains object references as recipe inputs across engines.
func (obj *ModuleObject) privateCollectionStateField(srv *dagql.Server) (dagql.Field[*ModuleObject], error) {
	module, provider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return dagql.Field[*ModuleObject]{}, err
	}
	installed := newInstalledServer(srv)
	return dagql.Field[*ModuleObject]{
		Spec: &dagql.FieldSpec{
			Name: privateCollectionStateFieldName, Type: obj,
			Module: module, ModuleProvider: provider, IsPersistable: true,
			GetDynamicInput: func(ctx context.Context, _ dagql.AnyResult, _ map[string]dagql.Input, _ call.View, req *dagql.CallRequest) error {
				return req.SetImplicitInput(ctx, "collectionStateType", dagql.String(obj.TypeDef.Name))
			},
			Args: dagql.NewInputSpecs(dagql.InputSpec{Name: "state", Type: dagql.InputObject[StateValue]{}}),
		},
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
			value, err := args["state"].(dagql.InputObject[StateValue]).Value.sdkValue()
			if err != nil {
				return nil, err
			}
			fields, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("collection state must be an object")
			}
			if _, exists := fields[collectionBaseField]; exists {
				return nil, fmt.Errorf("collection state cannot replace its original base")
			}
			copy := *self.Self()
			copy.Fields = fields
			copy.ArtifactView = nil
			if _, err := copy.collectionKeys(ctx); err != nil {
				return nil, err
			}
			srv, err := installed.forObject(ctx, self)
			if err != nil {
				return nil, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &copy)
		},
	}, nil
}
