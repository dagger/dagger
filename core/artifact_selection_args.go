package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// A check can request one of its enclosing collections as an explicit input.
// Every item under that collection receives the same filtered receiver. This
// gives ordinary module helpers the selection without re-entering their API.
func (node *ModTreeNode) collectionArgumentAncestor(arg *FunctionArg) *ModTreeNode {
	if !argRequired(arg) || arg.TypeDef.Self().Kind != TypeDefKindObject {
		return nil
	}
	name := arg.TypeDef.Self().ToType().Name()
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		obj := parent.ObjectType()
		if obj != nil && obj.Name == name && obj.Collection != nil && obj.Collection.Enabled {
			return parent
		}
	}
	return nil
}

func (node *ModTreeNode) functionRequiresCallerArgs(fn *Function) bool {
	if !fn.IsCheck {
		return functionRequiresCallerArgs(fn)
	}
	for _, arg := range fn.Args {
		if argRequired(arg.Self()) && node.collectionArgumentAncestor(arg.Self()) == nil {
			return true
		}
	}
	return false
}

func (node *ModTreeNode) boundArtifactArgs(ctx context.Context, srv *dagql.Server, fn *Function, supplied []dagql.NamedInput) ([]dagql.NamedInput, error) {
	args, err := boundWorkspaceArgs(ctx, srv, fn)
	if err != nil || fn == nil || !fn.IsCheck {
		return args, err
	}
	for _, arg := range fn.Args {
		provided := false
		for _, input := range supplied {
			provided = provided || input.Name == arg.Self().Name
		}
		if provided {
			continue
		}
		ancestor := node.collectionArgumentAncestor(arg.Self())
		if ancestor == nil {
			continue
		}
		var selected dagql.AnyObjectResult
		if err := ancestor.DagqlValue(ctx, &selected); err != nil {
			return nil, fmt.Errorf("bind selected collection %q: %w", arg.Self().Name, err)
		}
		id, err := selected.ID()
		if err != nil {
			return nil, err
		}
		encoded, err := id.Encode()
		if err != nil {
			return nil, err
		}
		input, err := arg.Self().TypeDef.Self().ToInput().Decoder().DecodeInput(encoded)
		if err != nil {
			return nil, err
		}
		args = append(args, dagql.NamedInput{Name: arg.Self().Name, Value: input})
	}
	return args, nil
}
