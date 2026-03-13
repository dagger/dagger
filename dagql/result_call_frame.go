package dagql

import (
	"context"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

type ResultCallFrameKind string

const (
	ResultCallFrameKindField     ResultCallFrameKind = "field"
	ResultCallFrameKindSynthetic ResultCallFrameKind = "synthetic"
)

type ResultCallFrameType struct {
	NamedType string               `json:"namedType,omitempty"`
	NonNull   bool                 `json:"nonNull,omitempty"`
	Elem      *ResultCallFrameType `json:"elem,omitempty"`
}

func NewResultCallFrameType(gqlType *ast.Type) *ResultCallFrameType {
	if gqlType == nil {
		return nil
	}
	return &ResultCallFrameType{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
		Elem:      NewResultCallFrameType(gqlType.Elem),
	}
}

func (typ *ResultCallFrameType) clone() *ResultCallFrameType {
	if typ == nil {
		return nil
	}
	return &ResultCallFrameType{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      typ.Elem.clone(),
	}
}

type ResultCallFrameRef struct {
	ResultID uint64 `json:"resultID,omitempty"`
}

type ResultCallFrameModule struct {
	ResultRef *ResultCallFrameRef `json:"resultRef,omitempty"`
	Name      string              `json:"name,omitempty"`
	Ref       string              `json:"ref,omitempty"`
	Pin       string              `json:"pin,omitempty"`
}

func (mod *ResultCallFrameModule) clone() *ResultCallFrameModule {
	if mod == nil {
		return nil
	}
	return &ResultCallFrameModule{
		ResultRef: mod.ResultRef.clone(),
		Name:      mod.Name,
		Ref:       mod.Ref,
		Pin:       mod.Pin,
	}
}

type ResultCallFrameArg struct {
	Name        string                  `json:"name,omitempty"`
	IsSensitive bool                    `json:"isSensitive,omitempty"`
	Value       *ResultCallFrameLiteral `json:"value,omitempty"`
}

func (arg *ResultCallFrameArg) clone() *ResultCallFrameArg {
	if arg == nil {
		return nil
	}
	return &ResultCallFrameArg{
		Name:        arg.Name,
		IsSensitive: arg.IsSensitive,
		Value:       arg.Value.clone(),
	}
}

type ResultCallFrameLiteralKind string

const (
	ResultCallFrameLiteralKindNull            ResultCallFrameLiteralKind = "null"
	ResultCallFrameLiteralKindBool            ResultCallFrameLiteralKind = "bool"
	ResultCallFrameLiteralKindInt             ResultCallFrameLiteralKind = "int"
	ResultCallFrameLiteralKindFloat           ResultCallFrameLiteralKind = "float"
	ResultCallFrameLiteralKindString          ResultCallFrameLiteralKind = "string"
	ResultCallFrameLiteralKindEnum            ResultCallFrameLiteralKind = "enum"
	ResultCallFrameLiteralKindDigestedString  ResultCallFrameLiteralKind = "digested_string"
	ResultCallFrameLiteralKindResultRef       ResultCallFrameLiteralKind = "result_ref"
	ResultCallFrameLiteralKindList            ResultCallFrameLiteralKind = "list"
	ResultCallFrameLiteralKindObject          ResultCallFrameLiteralKind = "object"
)

type ResultCallFrameLiteral struct {
	Kind ResultCallFrameLiteralKind `json:"kind"`

	BoolValue   bool    `json:"boolValue,omitempty"`
	IntValue    int64   `json:"intValue,omitempty"`
	FloatValue  float64 `json:"floatValue,omitempty"`
	StringValue string  `json:"stringValue,omitempty"`
	EnumValue   string  `json:"enumValue,omitempty"`

	DigestedStringValue  string        `json:"digestedStringValue,omitempty"`
	DigestedStringDigest digest.Digest `json:"digestedStringDigest,omitempty"`

	ResultRef *ResultCallFrameRef      `json:"resultRef,omitempty"`
	ListItems []*ResultCallFrameLiteral `json:"listItems,omitempty"`
	ObjectFields []*ResultCallFrameArg  `json:"objectFields,omitempty"`
}

func (lit *ResultCallFrameLiteral) clone() *ResultCallFrameLiteral {
	if lit == nil {
		return nil
	}
	cp := &ResultCallFrameLiteral{
		Kind:                 lit.Kind,
		BoolValue:            lit.BoolValue,
		IntValue:             lit.IntValue,
		FloatValue:           lit.FloatValue,
		StringValue:          lit.StringValue,
		EnumValue:            lit.EnumValue,
		DigestedStringValue:  lit.DigestedStringValue,
		DigestedStringDigest: lit.DigestedStringDigest,
		ResultRef:            lit.ResultRef.clone(),
	}
	if len(lit.ListItems) > 0 {
		cp.ListItems = make([]*ResultCallFrameLiteral, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			cp.ListItems = append(cp.ListItems, item.clone())
		}
	}
	if len(lit.ObjectFields) > 0 {
		cp.ObjectFields = make([]*ResultCallFrameArg, 0, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			cp.ObjectFields = append(cp.ObjectFields, field.clone())
		}
	}
	return cp
}

type ResultCallFrame struct {
	Kind        ResultCallFrameKind     `json:"kind"`
	Type        *ResultCallFrameType    `json:"type,omitempty"`
	Field       string                  `json:"field,omitempty"`
	SyntheticOp string                  `json:"syntheticOp,omitempty"`
	View        call.View               `json:"view,omitempty"`
	Nth         int64                   `json:"nth,omitempty"`
	EffectIDs   []string                `json:"effectIDs,omitempty"`
	Receiver    *ResultCallFrameRef     `json:"receiver,omitempty"`
	Module      *ResultCallFrameModule  `json:"module,omitempty"`
	Args        []*ResultCallFrameArg   `json:"args,omitempty"`
	ImplicitInputs []*ResultCallFrameArg `json:"implicitInputs,omitempty"`
}

func (frame *ResultCallFrame) clone() *ResultCallFrame {
	if frame == nil {
		return nil
	}
	cp := &ResultCallFrame{
		Kind:        frame.Kind,
		Type:        frame.Type.clone(),
		Field:       frame.Field,
		SyntheticOp: frame.SyntheticOp,
		View:        frame.View,
		Nth:         frame.Nth,
		EffectIDs:   slices.Clone(frame.EffectIDs),
		Receiver:    frame.Receiver.clone(),
		Module:      frame.Module.clone(),
	}
	if len(frame.Args) > 0 {
		cp.Args = make([]*ResultCallFrameArg, 0, len(frame.Args))
		for _, arg := range frame.Args {
			cp.Args = append(cp.Args, arg.clone())
		}
	}
	if len(frame.ImplicitInputs) > 0 {
		cp.ImplicitInputs = make([]*ResultCallFrameArg, 0, len(frame.ImplicitInputs))
		for _, arg := range frame.ImplicitInputs {
			cp.ImplicitInputs = append(cp.ImplicitInputs, arg.clone())
		}
	}
	return cp
}

func (ref *ResultCallFrameRef) clone() *ResultCallFrameRef {
	if ref == nil {
		return nil
	}
	return &ResultCallFrameRef{ResultID: ref.ResultID}
}

func (c *cache) resultCallFrameForIDLocked(ctx context.Context, id *call.ID) (*ResultCallFrame, error) {
	if id == nil {
		return nil, nil
	}
	frame := &ResultCallFrame{
		Kind:        ResultCallFrameKindField,
		Type:        NewResultCallFrameType(id.Type().ToAST()),
		Field:       id.Field(),
		View:        id.View(),
		Nth:         id.Nth(),
		EffectIDs:   slices.Clone(id.EffectIDs()),
	}
	if id.Receiver() != nil {
		ref, err := c.resultCallFrameRefForInputIDLocked(ctx, id.Receiver())
		if err != nil {
			return nil, fmt.Errorf("frame receiver %s: %w", id.Receiver().Digest(), err)
		}
		frame.Receiver = ref
	}
	if id.Module() != nil {
		mod := &ResultCallFrameModule{
			Name: id.Module().Name(),
			Ref:  id.Module().Ref(),
			Pin:  id.Module().Pin(),
		}
		if id.Module().ID() != nil {
			ref, err := c.resultCallFrameRefForInputIDLocked(ctx, id.Module().ID())
			if err != nil {
				return nil, fmt.Errorf("frame module %s: %w", id.Module().ID().Digest(), err)
			}
			mod.ResultRef = ref
		}
		frame.Module = mod
	}
	for _, arg := range id.Args() {
		lit, err := c.resultCallFrameLiteralFromCallLiteralLocked(ctx, arg.Value())
		if err != nil {
			return nil, fmt.Errorf("frame arg %q: %w", arg.Name(), err)
		}
		frame.Args = append(frame.Args, &ResultCallFrameArg{
			Name:        arg.Name(),
			IsSensitive: arg.IsSensitive(),
			Value:       lit,
		})
	}
	for _, arg := range id.ImplicitInputs() {
		lit, err := c.resultCallFrameLiteralFromCallLiteralLocked(ctx, arg.Value())
		if err != nil {
			return nil, fmt.Errorf("frame implicit input %q: %w", arg.Name(), err)
		}
		frame.ImplicitInputs = append(frame.ImplicitInputs, &ResultCallFrameArg{
			Name:        arg.Name(),
			IsSensitive: arg.IsSensitive(),
			Value:       lit,
		})
	}
	return frame, nil
}

func (c *cache) resultCallFrameLiteralFromCallLiteralLocked(
	ctx context.Context,
	lit call.Literal,
) (*ResultCallFrameLiteral, error) {
	switch v := lit.(type) {
	case nil:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
	case *call.LiteralNull:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
	case *call.LiteralBool:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindBool, BoolValue: v.Value()}, nil
	case *call.LiteralInt:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindInt, IntValue: v.Value()}, nil
	case *call.LiteralFloat:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindFloat, FloatValue: v.Value()}, nil
	case *call.LiteralString:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindString, StringValue: v.Value()}, nil
	case *call.LiteralEnum:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindEnum, EnumValue: v.Value()}, nil
	case *call.LiteralDigestedString:
		return &ResultCallFrameLiteral{
			Kind:                 ResultCallFrameLiteralKindDigestedString,
			DigestedStringValue:  v.Value(),
			DigestedStringDigest: v.Digest(),
		}, nil
	case *call.LiteralID:
		ref, err := c.resultCallFrameRefForInputIDLocked(ctx, v.Value())
		if err != nil {
			return nil, fmt.Errorf("frame literal id %s: %w", v.Value().Digest(), err)
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	case *call.LiteralList:
		items := make([]*ResultCallFrameLiteral, 0, v.Len())
		for _, item := range v.Values() {
			converted, err := c.resultCallFrameLiteralFromCallLiteralLocked(ctx, item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindList,
			ListItems: items,
		}, nil
	case *call.LiteralObject:
		fields := make([]*ResultCallFrameArg, 0, v.Len())
		for _, field := range v.Args() {
			converted, err := c.resultCallFrameLiteralFromCallLiteralLocked(ctx, field.Value())
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name(), err)
			}
			fields = append(fields, &ResultCallFrameArg{
				Name:        field.Name(),
				IsSensitive: field.IsSensitive(),
				Value:       converted,
			})
		}
		return &ResultCallFrameLiteral{
			Kind:         ResultCallFrameLiteralKindObject,
			ObjectFields: fields,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported literal %T", lit)
	}
}

func (c *cache) resultCallFrameRefForInputIDLocked(ctx context.Context, inputID *call.ID) (*ResultCallFrameRef, error) {
	if inputID == nil {
		return nil, nil
	}
	shared, err := c.resolveSharedResultForInputIDLocked(ctx, inputID)
	if err != nil {
		return nil, err
	}
	if shared == nil || shared.id == 0 {
		return nil, fmt.Errorf("missing shared result")
	}
	return &ResultCallFrameRef{ResultID: uint64(shared.id)}, nil
}

func (c *cache) ensureResultCallFrameLocked(ctx context.Context, res *sharedResult, id *call.ID) error {
	if res == nil || res.resultCallFrame != nil || id == nil {
		return nil
	}
	frame, err := c.resultCallFrameForIDLocked(ctx, id)
	if err != nil {
		return err
	}
	res.resultCallFrame = frame
	return nil
}
