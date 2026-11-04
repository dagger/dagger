package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// persistedCheck is a Check's stored form. Its function's inputs are stored
// as their literals and decoded through the function's argument types, so
// a restored check selects its body with the same arguments.
type persistedCheck struct {
	Assertion       *string               `json:"assertion,omitempty"`
	Report          uint64                `json:"report,omitempty"`
	RemoteArtifact  uint64                `json:"remoteArtifact,omitempty"`
	RemoteArguments json.RawMessage       `json:"remoteArguments,omitempty"`
	Unaddressed     uint64                `json:"unaddressed,omitempty"`
	Receiver        uint64                `json:"receiver,omitempty"`
	Workspace       uint64                `json:"workspace,omitempty"`
	Function        string                `json:"function,omitempty"`
	Address         string                `json:"address,omitempty"`
	Inputs          []persistedCheckInput `json:"inputs,omitempty"`
	CacheTTL        int64                 `json:"cacheTTL,omitempty"`
	Failure         string                `json:"failure,omitempty"`
	Completed       bool                  `json:"completed,omitempty"`
	Passed          bool                  `json:"passed,omitempty"`
	Error           uint64                `json:"error,omitempty"`
	StaleGenerator  string                `json:"staleGenerator,omitempty"`
	StalePatch      string                `json:"stalePatch,omitempty"`
}

type persistedCheckInput struct {
	Name  string                     `json:"name"`
	Value persistedModuleObjectValue `json:"value"`
}

var persistedCheckVisitor = persistedStructVisitor("", func(p *persistedCheck, w *persistedRefWalker) error {
	for _, ref := range []struct {
		field string
		id    *uint64
	}{
		{"report", &p.Report},
		{"remoteArtifact", &p.RemoteArtifact},
		{"unaddressed", &p.Unaddressed},
		{"receiver", &p.Receiver},
		{"workspace", &p.Workspace},
		{"error", &p.Error},
	} {
		if err := w.child(ref.field, ref.id); err != nil {
			return err
		}
	}
	for i := range p.Inputs {
		if err := visitPersistedModuleObjectValue(w.at("inputs").index(i).at("value"), &p.Inputs[i].Value); err != nil {
			return fmt.Errorf("input %q: %w", p.Inputs[i].Name, err)
		}
	}
	return nil
})

func (c *Check) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	// A generator's check reports the generator's state on every run, so
	// it is never kept.
	if c.Generator.Self() != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted check: a generator's check has no stored form")
	}
	payload := persistedCheck{
		Function:        c.Function,
		Address:         c.Address,
		RemoteArguments: json.RawMessage(c.RemoteArguments),
		CacheTTL:        c.CacheTTL,
		Failure:         c.Failure,
		Completed:       c.Completed,
		Passed:          c.Passed,
		StaleGenerator:  c.StaleGenerator,
		StalePatch:      c.StalePatch,
	}
	if c.Assertion.Valid {
		assertion := c.Assertion.Value.String()
		payload.Assertion = &assertion
	}
	var err error
	if c.Unaddressed.Self() != nil {
		if payload.Unaddressed, err = encodePersistedObjectRef(enc, c.Unaddressed, "check unaddressed"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	if c.RemoteArtifact.Self() != nil {
		if payload.RemoteArtifact, err = encodePersistedObjectRef(enc, c.RemoteArtifact, "check remote artifact"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	if c.Receiver.Self() != nil {
		if payload.Receiver, err = encodePersistedObjectRef(enc, c.Receiver, "check receiver"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	if c.Workspace.Self() != nil {
		if payload.Workspace, err = encodePersistedObjectRef(enc, c.Workspace, "check workspace"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	if c.Report.Valid {
		if payload.Report, err = encodePersistedObjectRef(enc, c.Report.Value, "check report"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	if c.Error.Valid {
		if payload.Error, err = encodePersistedObjectRef(enc, c.Error.Value, "check error"); err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	for _, input := range c.Inputs {
		value, ok := input.Value.(dagql.Input)
		if !ok {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted check input %q: %T is not an input", input.Name, input.Value)
		}
		plain, err := persistedCheckInputValue(value)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted check input %q: %w", input.Name, err)
		}
		encoded, err := encodePersistedModuleObjectValue(enc, plain)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted check input %q: %w", input.Name, err)
		}
		payload.Inputs = append(payload.Inputs, persistedCheckInput{Name: input.Name, Value: encoded})
	}
	return encodePersistedObjectPayload(payload)
}

// attachInputs owns the objects the check's inputs name, so a kept check
// keeps what its body runs with, as a module object owns the objects its
// fields name (see attachModuleObjectValue). An input whose object attaches
// as another result names that one instead.
func (c *Check) attachInputs(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]CallInput, []dagql.AnyResult, error) {
	var owned []dagql.AnyResult
	inputs := make([]CallInput, 0, len(c.Inputs))
	for _, input := range c.Inputs {
		plain, err := persistedCheckInputValue(input.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("check input %q: %w", input.Name, err)
		}
		attached, deps, changed, err := attachCheckInputIDs(ctx, attach, plain)
		if err != nil {
			return nil, nil, fmt.Errorf("check input %q: %w", input.Name, err)
		}
		owned = append(owned, deps...)
		if changed {
			value, err := c.decodeInput(input.Name, attached)
			if err != nil {
				return nil, nil, err
			}
			input.Value = value
		}
		inputs = append(inputs, input)
	}
	return inputs, owned, nil
}

// attachCheckInputIDs attaches the results named by the handle IDs in an
// input's plain value. A replaced ID is the attached result's own.
func attachCheckInputIDs(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error), plain any) (any, []dagql.AnyResult, bool, error) {
	switch x := plain.(type) {
	case dagql.IDable:
		id, err := x.ID()
		if err != nil {
			return nil, nil, false, err
		}
		value, deps, err := attachModuleObjectHandleValue(ctx, attach, x, id)
		if err != nil || len(deps) == 0 {
			return x, deps, false, err
		}
		attachedID, err := value.(dagql.AnyResult).ID()
		if err != nil {
			return nil, nil, false, err
		}
		if attachedID.EngineResultID() == id.EngineResultID() {
			return x, deps, false, nil
		}
		return attachedID, deps, true, nil
	case []any:
		items := make([]any, len(x))
		var owned []dagql.AnyResult
		changed := false
		for i, item := range x {
			value, deps, itemChanged, err := attachCheckInputIDs(ctx, attach, item)
			if err != nil {
				return nil, nil, false, err
			}
			items[i] = value
			owned = append(owned, deps...)
			changed = changed || itemChanged
		}
		return items, owned, changed, nil
	default:
		return plain, nil, false, nil
	}
}

// decodeInput decodes an input's plain value through the check function's
// argument type, as the function's own field does.
func (c *Check) decodeInput(name string, value any) (dagql.Input, error) {
	if c.Receiver.Self() == nil {
		return nil, fmt.Errorf("check input %q without a receiver", name)
	}
	fn, ok := c.Receiver.Self().TypeDef.FunctionByName(c.Function)
	if !ok {
		return nil, fmt.Errorf("check function %q is missing", c.Function)
	}
	for _, arg := range fn.Args {
		if arg.Self().Name == name {
			input, err := arg.Self().TypeDef.Self().ToInput().Decoder().DecodeInput(value)
			if err != nil {
				return nil, fmt.Errorf("check input %q: %w", name, err)
			}
			return input, nil
		}
	}
	return nil, fmt.Errorf("check function %q has no argument %q", c.Function, name)
}

// persistedCheckInputValue is an input as its argument type decodes it: a
// list's elements, an optional's value or null, an object's ID, which the
// encode context stores in either form, and a scalar's literal value.
func persistedCheckInputValue(input dagql.Typed) (any, error) {
	switch x := input.(type) {
	case dagql.IDable:
		return x, nil
	case dagql.Derefable:
		inner, ok := x.Deref()
		if !ok {
			return nil, nil
		}
		return persistedCheckInputValue(inner)
	case dagql.Enumerable:
		items := make([]any, 0, x.Len())
		for i := 1; i <= x.Len(); i++ {
			item, err := x.Nth(i)
			if err != nil {
				return nil, err
			}
			value, err := persistedCheckInputValue(item)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		return items, nil
	case dagql.Input:
		return x.ToLiteral().ToInput(), nil
	default:
		return nil, fmt.Errorf("%T is not an input", input)
	}
}

func (*Check) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	var payload persistedCheck
	if err := unmarshalPersistedPayload(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode persisted check: %w", err)
	}
	c := &Check{
		Function:        payload.Function,
		Address:         payload.Address,
		RemoteArguments: JSON(payload.RemoteArguments),
		CacheTTL:        payload.CacheTTL,
		Failure:         payload.Failure,
		Completed:       payload.Completed,
		Passed:          payload.Passed,
		StaleGenerator:  payload.StaleGenerator,
		StalePatch:      payload.StalePatch,
	}
	if payload.Assertion != nil {
		c.Assertion = dagql.NonNull(dagql.String(*payload.Assertion))
	}
	var err error
	if c.Unaddressed, err = loadPersistedObjectResultByResultID[*Check](ctx, dec, payload.Unaddressed, "check unaddressed"); err != nil {
		return nil, err
	}
	if c.RemoteArtifact, err = loadPersistedObjectResultByResultID[*Artifact](ctx, dec, payload.RemoteArtifact, "check remote artifact"); err != nil {
		return nil, err
	}
	if c.Receiver, err = loadPersistedObjectResultByResultID[*ModuleObject](ctx, dec, payload.Receiver, "check receiver"); err != nil {
		return nil, err
	}
	if c.Workspace, err = loadPersistedObjectResultByResultID[*Workspace](ctx, dec, payload.Workspace, "check workspace"); err != nil {
		return nil, err
	}
	if payload.Report != 0 {
		report, err := loadPersistedObjectResultByResultID[*Directory](ctx, dec, payload.Report, "check report")
		if err != nil {
			return nil, err
		}
		c.Report = dagql.NonNull(report)
	}
	if payload.Error != 0 {
		checkErr, err := loadPersistedObjectResultByResultID[*Error](ctx, dec, payload.Error, "check error")
		if err != nil {
			return nil, err
		}
		c.Error = dagql.NonNull(checkErr)
	}
	for _, input := range payload.Inputs {
		value, err := decodePersistedModuleObjectValue(ctx, dec, input.Value)
		if err != nil {
			return nil, fmt.Errorf("decode persisted check input %q: %w", input.Name, err)
		}
		typed, err := c.decodeInput(input.Name, value)
		if err != nil {
			return nil, fmt.Errorf("decode persisted check: %w", err)
		}
		c.Inputs = append(c.Inputs, CallInput{Name: input.Name, Value: typed})
	}
	return c, nil
}
