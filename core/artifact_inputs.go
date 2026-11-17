package core

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

func (a *Artifact) Arguments() dagql.ObjectResultArray[*FunctionArg] {
	if a.Node != nil && a.Node.Parent != nil {
		if obj := a.Node.Parent.ObjectType(); obj != nil {
			if fn, ok := obj.FunctionByName(a.Node.Name); ok {
				return fn.Args
			}
		}
	}
	return dagql.ObjectResultArray[*FunctionArg]{}
}

func (a *Artifact) CallInputs(raw JSON) ([]dagql.NamedInput, error) {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("artifact arguments: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	inputs := make([]dagql.NamedInput, 0, len(values))
	for _, arg := range a.Arguments() {
		value, ok := values[arg.Self().Name]
		if !ok {
			continue
		}
		input, err := arg.Self().TypeDef.Self().ToInput().Decoder().DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", arg.Self().Name, err)
		}
		inputs = append(inputs, dagql.NamedInput{Name: arg.Self().Name, Value: input})
		delete(values, arg.Self().Name)
	}
	if len(values) != 0 {
		return nil, fmt.Errorf("unknown artifact arguments: %v", values)
	}
	return inputs, nil
}
