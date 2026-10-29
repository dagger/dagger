package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// runFunction runs the author function only when a check result is
// requested. It selects the check's hidden run field, which is cached like
// the function itself, so an unchanged check is a cache hit.
func (c *Check) runFunction(ctx context.Context) error {
	receiver := c.Receiver.Self()
	srv, err := dagqlServerForModule(ctx, receiver.Module)
	if err != nil {
		return err
	}
	if c.Workspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, c.Workspace)
	}
	if _, ok := receiver.TypeDef.FunctionByName(c.Function); !ok {
		return fmt.Errorf("check function %q is missing", c.Function)
	}
	args := make([]dagql.NamedInput, 0, len(c.Inputs))
	for _, input := range c.Inputs {
		value, ok := input.Value.(dagql.Input)
		if !ok {
			return fmt.Errorf("check %q argument %q is %T, not an input", c.Function, input.Name, input.Value)
		}
		args = append(args, dagql.NamedInput{Name: input.Name, Value: value})
	}
	var result dagql.AnyResult
	return srv.Select(ctx, c.Receiver, &result, dagql.Selector{
		Field: checkRunFieldName(c.Function),
		Args:  args,
	})
}

// checkRunFieldName is the hidden field that runs a check function's body.
func checkRunFieldName(function string) string {
	return "__check_" + function
}

func (c *Check) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	var owned []dagql.AnyResult
	if c.RemoteArtifact.Self() != nil {
		result, err := attach(c.RemoteArtifact)
		if err != nil {
			return nil, err
		}
		c.RemoteArtifact = result.(dagql.ObjectResult[*Artifact])
		owned = append(owned, result)
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
	var outcome struct {
		Sync struct {
			Pass  bool
			Error *Error
		}
	}
	if err := queryArtifactCloud(ctx, c.RemoteArtifact, c.RemoteArguments, `... on Check { sync { pass error { message values { name value } } } }`, &outcome); err != nil {
		return err
	}
	if outcome.Sync.Error != nil {
		return outcome.Sync.Error
	}
	if !outcome.Sync.Pass {
		return fmt.Errorf("check failed")
	}
	return nil
}
