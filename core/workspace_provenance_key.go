package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// workspaceCallKey groups candidate observations without the state of direct
// Workspace inputs. It does not prove that an observation is valid for another
// workspace. The receiver, module implementation and every other input retain
// their content identities, including historical workspace dependencies.
func workspaceCallKey(ctx context.Context, req *dagql.CallRequest) (string, error) {
	if req == nil || req.ResultCall == nil {
		return "", fmt.Errorf("missing call")
	}
	if req.DoNotCache {
		return "", nil
	}
	for _, input := range req.ImplicitInputs {
		if input != nil && (input.Name == dagql.PerCallInput.Name || input.Name == dagql.PerSessionInput.Name) {
			return "", nil
		}
	}
	frame := req.ResultCall.Clone()
	frame.ExtraDigests = nil
	if err := visitWorkspaceInputRefs(ctx, frame, func(ref *dagql.ResultCallRef) error {
		*ref = dagql.ResultCallRef{Call: &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "workspace-call-input.v1",
			Type:        dagql.NewResultCallType((*Workspace)(nil).Type()),
		}}
		return nil
	}); err != nil {
		return "", err
	}
	key, err := frame.ContentPreferredDigest(ctx)
	if err != nil {
		return "", err
	}
	return key.String(), nil
}

// workspaceRefs returns direct Workspace references in argument and implicit
// input literals. It does not traverse the receiver or the recipes behind other
// argument values. The references belong to frame and must not be modified.
func workspaceRefs(ctx context.Context, frame *dagql.ResultCall) ([]*dagql.ResultCallRef, error) {
	var refs []*dagql.ResultCallRef
	err := visitWorkspaceInputRefs(ctx, frame, func(ref *dagql.ResultCallRef) error {
		refs = append(refs, ref)
		return nil
	})
	return refs, err
}

func visitWorkspaceInputRefs(ctx context.Context, frame *dagql.ResultCall, visit func(*dagql.ResultCallRef) error) error {
	if frame == nil {
		return nil
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	var literal func(*dagql.ResultCallLiteral) error
	literal = func(value *dagql.ResultCallLiteral) error {
		if value == nil {
			return nil
		}
		switch value.Kind {
		case dagql.ResultCallLiteralKindResultRef:
			input, err := cache.ResultCallForRef(value.ResultRef)
			if err != nil {
				return err
			}
			if input != nil && input.Type != nil && input.Type.Elem == nil && input.Type.NamedType == "Workspace" {
				return visit(value.ResultRef)
			}
		case dagql.ResultCallLiteralKindList:
			for _, item := range value.ListItems {
				if err := literal(item); err != nil {
					return err
				}
			}
		case dagql.ResultCallLiteralKindObject:
			for _, field := range value.ObjectFields {
				if field != nil {
					if err := literal(field.Value); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, inputs := range [][]*dagql.ResultCallArg{frame.Args, frame.ImplicitInputs} {
		for _, input := range inputs {
			if input != nil {
				if err := literal(input.Value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
