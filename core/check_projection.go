package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// runFunction invokes the author function only when a check result is requested.
func (c *Check) runFunction(ctx context.Context) error {
	receiver := c.Receiver.Self()
	srv, err := dagqlServerForModule(ctx, receiver.Module)
	if err != nil {
		return err
	}
	if c.Workspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, c.Workspace)
	}
	fn, ok := receiver.TypeDef.FunctionByName(c.Function)
	if !ok {
		return fmt.Errorf("check function %q is missing", c.Function)
	}
	callable, err := NewModFunction(ctx, receiver.Module, receiver.TypeDef, fn)
	if err != nil {
		return err
	}
	result, err := callable.Call(ctx, &CallOpts{
		ParentTyped: c.Receiver, ParentFields: receiver.Fields, Inputs: c.Inputs, Server: srv,
	})
	if err != nil {
		return err
	}
	if obj, ok := result.(dagql.AnyObjectResult); ok {
		if sync, ok := obj.ObjectType().FieldSpec("sync", srv.View); ok && !sync.Args.HasRequired(srv.View) {
			return srv.Select(ctx, obj, &result, dagql.Selector{Field: "sync"})
		}
	}
	return err
}

func (c *Check) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	var owned []dagql.AnyResult
	if c.RemoteArtifact != nil {
		deps, err := c.RemoteArtifact.AttachDependencyResults(ctx, nil, attach)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}
	if c.Receiver.Self() != nil {
		result, err := attach(c.Receiver)
		if err != nil {
			return nil, err
		}
		c.Receiver = result.(dagql.ObjectResult[*ModuleObject])
		owned = append(owned, result)
	}
	if c.Generator.Self() != nil {
		result, err := attach(c.Generator)
		if err != nil {
			return nil, err
		}
		c.Generator = result.(dagql.ObjectResult[*Generator])
		owned = append(owned, result)
	}
	if c.Workspace.Self() != nil {
		result, err := attach(c.Workspace)
		if err != nil {
			return nil, err
		}
		c.Workspace = result.(dagql.ObjectResult[*Workspace])
		owned = append(owned, result)
	}
	if c.Report.Valid {
		result, err := attach(c.Report.Value)
		if err != nil {
			return nil, err
		}
		c.Report.Value = result.(dagql.ObjectResult[*Directory])
		owned = append(owned, result)
	}
	if c.Error.Valid {
		result, err := attach(c.Error.Value)
		if err != nil {
			return nil, err
		}
		c.Error.Value = result.(dagql.ObjectResult[*Error])
		owned = append(owned, result)
	}
	return owned, nil
}

func (c *Check) runRemote(ctx context.Context) error {
	var outcome struct{ Pass bool }
	if err := c.RemoteArtifact.QueryCloud(ctx, c.RemoteArguments, `... on Check { pass }`, &outcome); err != nil {
		return err
	}
	if !outcome.Pass {
		return fmt.Errorf("check failed")
	}
	return nil
}
