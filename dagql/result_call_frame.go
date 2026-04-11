package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
)

type ResultCallKind string

const (
	ResultCallKindField     ResultCallKind = "field"
	ResultCallKindSynthetic ResultCallKind = "synthetic"
)

type ResultCallType struct {
	NamedType string          `json:"namedType,omitempty"`
	NonNull   bool            `json:"nonNull,omitempty"`
	Elem      *ResultCallType `json:"elem,omitempty"`
}

func NewResultCallType(gqlType *ast.Type) *ResultCallType {
	if gqlType == nil {
		return nil
	}
	return &ResultCallType{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
		Elem:      NewResultCallType(gqlType.Elem),
	}
}

func (typ *ResultCallType) clone() *ResultCallType {
	if typ == nil {
		return nil
	}
	return &ResultCallType{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      typ.Elem.clone(),
	}
}

func (typ *ResultCallType) toAST() *ast.Type {
	if typ == nil {
		return nil
	}
	return &ast.Type{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      typ.Elem.toAST(),
	}
}

type ResultCallRef struct {
	ResultID uint64      `json:"resultID,omitempty"`
	Call     *ResultCall `json:"call,omitempty"`

	// shared is a runtime-only fast path for attached result refs. It is not
	// persisted and must never be the sole source of truth for identity.
	shared *sharedResult
}

type ResultCallModule struct {
	ResultRef *ResultCallRef `json:"resultRef,omitempty"`
	Name      string         `json:"name,omitempty"`
	Ref       string         `json:"ref,omitempty"`
	Pin       string         `json:"pin,omitempty"`
}

func (mod *ResultCallModule) clone() *ResultCallModule {
	if mod == nil {
		return nil
	}
	return &ResultCallModule{
		ResultRef: mod.ResultRef.clone(),
		Name:      mod.Name,
		Ref:       mod.Ref,
		Pin:       mod.Pin,
	}
}

type ResultCallArg struct {
	Name        string             `json:"name,omitempty"`
	IsSensitive bool               `json:"isSensitive,omitempty"`
	Value       *ResultCallLiteral `json:"value,omitempty"`
}

func (arg *ResultCallArg) clone() *ResultCallArg {
	if arg == nil {
		return nil
	}
	return &ResultCallArg{
		Name:        arg.Name,
		IsSensitive: arg.IsSensitive,
		Value:       arg.Value.clone(),
	}
}

type ResultCallLiteralKind string

const (
	ResultCallLiteralKindNull           ResultCallLiteralKind = "null"
	ResultCallLiteralKindBool           ResultCallLiteralKind = "bool"
	ResultCallLiteralKindInt            ResultCallLiteralKind = "int"
	ResultCallLiteralKindFloat          ResultCallLiteralKind = "float"
	ResultCallLiteralKindString         ResultCallLiteralKind = "string"
	ResultCallLiteralKindEnum           ResultCallLiteralKind = "enum"
	ResultCallLiteralKindDigestedString ResultCallLiteralKind = "digested_string"
	ResultCallLiteralKindResultRef      ResultCallLiteralKind = "result_ref"
	ResultCallLiteralKindList           ResultCallLiteralKind = "list"
	ResultCallLiteralKindObject         ResultCallLiteralKind = "object"
)

type ResultCallLiteral struct {
	Kind ResultCallLiteralKind `json:"kind"`

	BoolValue   bool    `json:"boolValue,omitempty"`
	IntValue    int64   `json:"intValue,omitempty"`
	FloatValue  float64 `json:"floatValue,omitempty"`
	StringValue string  `json:"stringValue,omitempty"`
	EnumValue   string  `json:"enumValue,omitempty"`

	DigestedStringValue  string        `json:"digestedStringValue,omitempty"`
	DigestedStringDigest digest.Digest `json:"digestedStringDigest,omitempty"`

	ResultRef    *ResultCallRef       `json:"resultRef,omitempty"`
	ListItems    []*ResultCallLiteral `json:"listItems,omitempty"`
	ObjectFields []*ResultCallArg     `json:"objectFields,omitempty"`
}

func (lit *ResultCallLiteral) clone() *ResultCallLiteral {
	if lit == nil {
		return nil
	}
	cp := &ResultCallLiteral{
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
		cp.ListItems = make([]*ResultCallLiteral, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			cp.ListItems = append(cp.ListItems, item.clone())
		}
	}
	if len(lit.ObjectFields) > 0 {
		cp.ObjectFields = make([]*ResultCallArg, 0, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			cp.ObjectFields = append(cp.ObjectFields, field.clone())
		}
	}
	return cp
}

type ResultCall struct {
	Kind        ResultCallKind  `json:"kind"`
	Type        *ResultCallType `json:"type,omitempty"`
	Field       string          `json:"field,omitempty"`
	SyntheticOp string          `json:"syntheticOp,omitempty"`
	View        call.View       `json:"view,omitempty"`
	Nth         int64           `json:"nth,omitempty"`
	EffectIDs   []string        `json:"effectIDs,omitempty"`
	// ExtraDigests are the original extra digests explicitly attached when this
	// call/result was first created. They are useful provenance, but they are
	// not the authoritative merged digest state. The cache/e-graph remains the
	// source of truth for the full merged output-equivalence digest set.
	ExtraDigests   []call.ExtraDigest `json:"extraDigests,omitempty"`
	Receiver       *ResultCallRef     `json:"receiver,omitempty"`
	Module         *ResultCallModule  `json:"module,omitempty"`
	Args           []*ResultCallArg   `json:"args,omitempty"`
	ImplicitInputs []*ResultCallArg   `json:"implicitInputs,omitempty"`

	// recipeDigest is memoized once the frame has reached its finalized
	// semantic shape. Do not mutate the frame after calling RecipeDigest.
	recipeDigestOnce sync.Once
	recipeDigestErr  error
	recipeDigest     digest.Digest

	// contentPreferredDigest is memoized once the frame has reached its
	// finalized semantic shape. Do not mutate the frame after calling
	// ContentPreferredDigest.
	contentPreferredDigestOnce sync.Once
	contentPreferredDigestErr  error
	contentPreferredDigest     digest.Digest
}

type ResultCallStructuralInputRef struct {
	Result *ResultCallRef
	Digest digest.Digest
}

func (frame *ResultCall) clone() *ResultCall {
	if frame == nil {
		return nil
	}
	cp := &ResultCall{
		Kind:         frame.Kind,
		Type:         frame.Type.clone(),
		Field:        frame.Field,
		SyntheticOp:  frame.SyntheticOp,
		View:         frame.View,
		Nth:          frame.Nth,
		EffectIDs:    slices.Clone(frame.EffectIDs),
		ExtraDigests: slices.Clone(frame.ExtraDigests),
		Receiver:     frame.Receiver.clone(),
		Module:       frame.Module.clone(),
	}
	if len(frame.Args) > 0 {
		cp.Args = make([]*ResultCallArg, 0, len(frame.Args))
		for _, arg := range frame.Args {
			cp.Args = append(cp.Args, arg.clone())
		}
	}
	if len(frame.ImplicitInputs) > 0 {
		cp.ImplicitInputs = make([]*ResultCallArg, 0, len(frame.ImplicitInputs))
		for _, arg := range frame.ImplicitInputs {
			cp.ImplicitInputs = append(cp.ImplicitInputs, arg.clone())
		}
	}
	return cp
}

func (frame *ResultCall) fork() *ResultCall {
	if frame == nil {
		return nil
	}
	return &ResultCall{
		Kind:           frame.Kind,
		Type:           frame.Type,
		Field:          frame.Field,
		SyntheticOp:    frame.SyntheticOp,
		View:           frame.View,
		Nth:            frame.Nth,
		EffectIDs:      slices.Clone(frame.EffectIDs),
		ExtraDigests:   slices.Clone(frame.ExtraDigests),
		Receiver:       frame.Receiver,
		Module:         frame.Module,
		Args:           slices.Clone(frame.Args),
		ImplicitInputs: slices.Clone(frame.ImplicitInputs),
	}
}

func (frame *ResultCall) RecipeDigest(ctx context.Context) (digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return frame.deriveRecipeDigest(c)
}

func (frame *ResultCall) deriveRecipeDigest(c *Cache) (digest.Digest, error) {
	return frame.recipeDigestWithVisiting(c, map[sharedResultID]struct{}{})
}

func (frame *ResultCall) ContentDigest() digest.Digest {
	if frame == nil {
		return ""
	}
	var last digest.Digest
	for _, extra := range frame.ExtraDigests {
		if extra.Label != call.ExtraDigestLabelContent || extra.Digest == "" {
			continue
		}
		last = extra.Digest
	}
	return last
}

func (frame *ResultCall) ContentPreferredDigest(ctx context.Context) (digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return frame.deriveContentPreferredDigest(c)
}

func (frame *ResultCall) deriveContentPreferredDigest(c *Cache) (digest.Digest, error) {
	return frame.contentPreferredDigestWithVisiting(c, map[sharedResultID]struct{}{})
}

func (frame *ResultCall) Inputs(ctx context.Context) ([]digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	return frame.inputs(c)
}

func (frame *ResultCall) inputs(c *Cache) ([]digest.Digest, error) {
	if frame == nil {
		return nil, nil
	}

	seen := map[digest.Digest]struct{}{}
	var inputs []digest.Digest
	see := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		if _, ok := seen[dig]; ok {
			return
		}
		seen[dig] = struct{}{}
		inputs = append(inputs, dig)
	}

	if frame.Receiver != nil {
		receiverDigest, err := recipeDigestForResultCallRef(c, frame.Receiver, map[sharedResultID]struct{}{})
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
		see(receiverDigest)
	}

	var walkLiteral func(*ResultCallLiteral) error
	walkLiteral = func(lit *ResultCallLiteral) error {
		if lit == nil {
			return nil
		}
		switch lit.Kind {
		case ResultCallLiteralKindResultRef:
			dig, err := recipeDigestForResultCallRef(c, lit.ResultRef, map[sharedResultID]struct{}{})
			if err != nil {
				return err
			}
			see(dig)
		case ResultCallLiteralKindDigestedString:
			see(lit.DigestedStringDigest)
		case ResultCallLiteralKindList:
			for _, item := range lit.ListItems {
				if err := walkLiteral(item); err != nil {
					return err
				}
			}
		case ResultCallLiteralKindObject:
			for _, field := range lit.ObjectFields {
				if field == nil {
					continue
				}
				if err := walkLiteral(field.Value); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, arg := range frame.Args {
		if arg == nil {
			continue
		}
		if err := walkLiteral(arg.Value); err != nil {
			return nil, fmt.Errorf("arg %q: %w", arg.Name, err)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		if err := walkLiteral(input.Value); err != nil {
			return nil, fmt.Errorf("implicit input %q: %w", input.Name, err)
		}
	}

	return inputs, nil
}

func (frame *ResultCall) RecipeID(ctx context.Context) (*call.ID, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	return frame.recipeIDWithContext(ctx, c)
}

func (frame *ResultCall) recipeID(c *Cache) (*call.ID, error) {
	return frame.recipeIDWithContext(context.Background(), c)
}

func (frame *ResultCall) recipeIDWithContext(ctx context.Context, c *Cache) (*call.ID, error) {
	return frame.recipeIDWithVisiting(ctx, c, map[sharedResultID]struct{}{})
}

func (frame *ResultCall) CallPB(ctx context.Context) (*callpbv1.Call, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	return frame.callPB(c)
}

func (frame *ResultCall) callPB(c *Cache) (*callpbv1.Call, error) {
	if frame == nil {
		return nil, fmt.Errorf("nil result call")
	}
	field, err := resultCallIdentityField(frame)
	if err != nil {
		return nil, err
	}
	if frame.Type == nil {
		return nil, fmt.Errorf("missing call type")
	}
	callDigest, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return nil, fmt.Errorf("call digest: %w", err)
	}

	pbCall := &callpbv1.Call{
		Type:      resultCallTypePB(frame.Type),
		Field:     field,
		Nth:       frame.Nth,
		View:      frame.View.String(),
		Digest:    callDigest.String(),
		EffectIds: slices.Clone(frame.EffectIDs),
	}
	if frame.Receiver != nil {
		receiverDigest, err := recipeDigestForResultCallRef(c, frame.Receiver, map[sharedResultID]struct{}{})
		if err != nil {
			return nil, fmt.Errorf("receiver digest: %w", err)
		}
		pbCall.ReceiverDigest = receiverDigest.String()
	}
	if frame.Module != nil && frame.Module.ResultRef != nil {
		moduleDigest, err := recipeDigestForResultCallRef(c, frame.Module.ResultRef, map[sharedResultID]struct{}{})
		if err != nil {
			return nil, fmt.Errorf("module digest: %w", err)
		}
		pbCall.Module = &callpbv1.Module{
			CallDigest: moduleDigest.String(),
			Name:       frame.Module.Name,
			Ref:        frame.Module.Ref,
			Pin:        frame.Module.Pin,
		}
	}
	for _, extra := range frame.ExtraDigests {
		if extra.Digest == "" {
			continue
		}
		pbCall.ExtraDigests = append(pbCall.ExtraDigests, &callpbv1.ExtraDigest{
			Digest: extra.Digest.String(),
			Label:  extra.Label,
		})
	}
	for _, arg := range frame.Args {
		if arg == nil {
			continue
		}
		pbArg, err := resultCallArgPB(c, arg)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", arg.Name, err)
		}
		pbCall.Args = append(pbCall.Args, pbArg)
	}
	for _, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		pbArg, err := resultCallArgPB(c, input)
		if err != nil {
			return nil, fmt.Errorf("implicit input %q: %w", input.Name, err)
		}
		pbCall.ImplicitInputs = append(pbCall.ImplicitInputs, pbArg)
	}
	return pbCall, nil
}

func resultCallTypePB(typ *ResultCallType) *callpbv1.Type {
	if typ == nil {
		return nil
	}
	return &callpbv1.Type{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      resultCallTypePB(typ.Elem),
	}
}

func resultCallArgPB(c *Cache, arg *ResultCallArg) (*callpbv1.Argument, error) {
	if arg == nil {
		return nil, nil
	}
	lit, err := resultCallLiteralPB(c, arg.Value)
	if err != nil {
		return nil, err
	}
	return &callpbv1.Argument{
		Name:  arg.Name,
		Value: lit,
	}, nil
}

func resultCallLiteralPB(c *Cache, lit *ResultCallLiteral) (*callpbv1.Literal, error) {
	if lit == nil {
		return &callpbv1.Literal{Value: &callpbv1.Literal_Null{Null: true}}, nil
	}
	switch lit.Kind {
	case ResultCallLiteralKindNull:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Null{Null: true}}, nil
	case ResultCallLiteralKindBool:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Bool{Bool: lit.BoolValue}}, nil
	case ResultCallLiteralKindInt:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Int{Int: lit.IntValue}}, nil
	case ResultCallLiteralKindFloat:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Float{Float: lit.FloatValue}}, nil
	case ResultCallLiteralKindString:
		return &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: lit.StringValue}}, nil
	case ResultCallLiteralKindEnum:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Enum{Enum: lit.EnumValue}}, nil
	case ResultCallLiteralKindDigestedString:
		return &callpbv1.Literal{Value: &callpbv1.Literal_DigestedString{
			DigestedString: &callpbv1.DigestedString{
				Value:  lit.DigestedStringValue,
				Digest: lit.DigestedStringDigest.String(),
			},
		}}, nil
	case ResultCallLiteralKindResultRef:
		dig, err := recipeDigestForResultCallRef(c, lit.ResultRef, map[sharedResultID]struct{}{})
		if err != nil {
			return nil, err
		}
		return &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: dig.String()}}, nil
	case ResultCallLiteralKindList:
		values := make([]*callpbv1.Literal, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			pbItem, err := resultCallLiteralPB(c, item)
			if err != nil {
				return nil, err
			}
			values = append(values, pbItem)
		}
		return &callpbv1.Literal{Value: &callpbv1.Literal_List{
			List: &callpbv1.List{Values: values},
		}}, nil
	case ResultCallLiteralKindObject:
		fields := make([]*callpbv1.Argument, 0, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			pbField, err := resultCallArgPB(c, field)
			if err != nil {
				return nil, err
			}
			fields = append(fields, pbField)
		}
		return &callpbv1.Literal{Value: &callpbv1.Literal_Object{
			Object: &callpbv1.Object{Values: fields},
		}}, nil
	default:
		return nil, fmt.Errorf("unknown result call literal kind %q", lit.Kind)
	}
}

func (frame *ResultCall) ReceiverCall(ctx context.Context) (*ResultCall, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	return frame.receiverCall(c)
}

func (frame *ResultCall) receiverCall(c *Cache) (*ResultCall, error) {
	if frame == nil || frame.Receiver == nil {
		return nil, nil
	}
	return frame.refCall(c, frame.Receiver)
}

func (frame *ResultCall) refCall(c *Cache, ref *ResultCallRef) (*ResultCall, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Call != nil {
		return ref.Call, nil
	}
	if target := ref.loadSharedCall(); target != nil {
		return target, nil
	}
	if c == nil {
		return nil, fmt.Errorf("cannot resolve result ref %d without cache", ref.ResultID)
	}
	target := c.resultCallByResultID(sharedResultID(ref.ResultID))
	if target == nil {
		return nil, fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
	}
	return target, nil
}

func (frame *ResultCall) recipeDigestWithVisiting(c *Cache, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}

	frame.recipeDigestOnce.Do(func() {
		field, err := resultCallIdentityField(frame)
		if err != nil {
			frame.recipeDigestErr = err
			return
		}
		if frame.Type == nil {
			frame.recipeDigestErr = fmt.Errorf("missing call type")
			return
		}

		h := hashutil.NewHasher()

		if frame.Receiver != nil {
			receiverDigest, err := recipeDigestForResultCallRef(c, frame.Receiver, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.recipeDigestErr = fmt.Errorf("receiver: %w", err)
				return
			}
			h = h.WithString(receiverDigest.String())
		}
		h = h.WithDelim()

		h = appendResultCallTypeBytes(h, frame.Type).
			WithDelim()

		h = h.WithString(field).
			WithDelim()

		for _, arg := range frame.Args {
			arg = redactedCallArgForDigest(arg)
			if arg == nil {
				continue
			}
			nextH, err := appendResultCallArgBytes(c, arg, h, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.recipeDigestErr = fmt.Errorf("args: %w", err)
				return
			}
			h = nextH
			h = h.WithDelim()
		}
		h = h.WithDelim()

		for _, input := range frame.ImplicitInputs {
			input = redactedCallArgForDigest(input)
			if input == nil {
				continue
			}
			nextH, err := appendResultCallArgBytes(c, input, h, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.recipeDigestErr = fmt.Errorf("implicit inputs: %w", err)
				return
			}
			h = nextH
			h = h.WithDelim()
		}
		h = h.WithDelim()

		if frame.Module != nil && frame.Module.ResultRef != nil {
			moduleDigest, err := recipeDigestForResultCallRef(c, frame.Module.ResultRef, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.recipeDigestErr = fmt.Errorf("module: %w", err)
				return
			}
			h = h.WithString(moduleDigest.String())
		}
		h = h.WithDelim()

		h = h.WithInt64(frame.Nth).
			WithDelim()

		h = h.WithString(frame.View.String()).
			WithDelim()

		frame.recipeDigest = digest.Digest(h.DigestAndClose())
	})
	return frame.recipeDigest, frame.recipeDigestErr
}

func (frame *ResultCall) contentPreferredDigestWithVisiting(c *Cache, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}

	frame.contentPreferredDigestOnce.Do(func() {
		if content := frame.ContentDigest(); content != "" {
			frame.contentPreferredDigest = content
			return
		}

		field, err := resultCallIdentityField(frame)
		if err != nil {
			frame.contentPreferredDigestErr = err
			return
		}
		if frame.Type == nil {
			frame.contentPreferredDigestErr = fmt.Errorf("missing call type")
			return
		}

		h := hashutil.NewHasher()

		if frame.Receiver != nil {
			receiverDigest, err := contentPreferredDigestForResultCallRef(c, frame.Receiver, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.contentPreferredDigestErr = fmt.Errorf("receiver: %w", err)
				return
			}
			h = h.WithString(receiverDigest.String())
		}
		h = h.WithDelim()

		h = appendResultCallTypeBytes(h, frame.Type).
			WithDelim()

		h = h.WithString(field).
			WithDelim()

		for _, arg := range frame.Args {
			arg = redactedCallArgForDigest(arg)
			if arg == nil {
				continue
			}
			nextH, err := appendResultCallArgContentPreferredBytes(c, arg, h, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.contentPreferredDigestErr = fmt.Errorf("args: %w", err)
				return
			}
			h = nextH
			h = h.WithDelim()
		}
		h = h.WithDelim()

		for _, input := range frame.ImplicitInputs {
			input = redactedCallArgForDigest(input)
			if input == nil {
				continue
			}
			nextH, err := appendResultCallArgContentPreferredBytes(c, input, h, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.contentPreferredDigestErr = fmt.Errorf("implicit inputs: %w", err)
				return
			}
			h = nextH
			h = h.WithDelim()
		}

		if frame.Module != nil && frame.Module.ResultRef != nil {
			moduleDigest, err := contentPreferredDigestForResultCallRef(c, frame.Module.ResultRef, visiting)
			if err != nil {
				if h != nil {
					h.Close()
				}
				frame.contentPreferredDigestErr = fmt.Errorf("module: %w", err)
				return
			}
			h = h.WithString(moduleDigest.String())
		}
		h = h.WithDelim()

		h = h.WithInt64(frame.Nth).
			WithDelim()

		h = h.WithString(frame.View.String()).
			WithDelim()

		frame.contentPreferredDigest = digest.Digest(h.DigestAndClose())
	})
	return frame.contentPreferredDigest, frame.contentPreferredDigestErr
}

func (frame *ResultCall) SelfDigestAndInputRefs(ctx context.Context) (digest.Digest, []ResultCallStructuralInputRef, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", nil, err
	}
	return frame.selfDigestAndInputRefs(c)
}

func (frame *ResultCall) selfDigestAndInputRefs(c *Cache) (digest.Digest, []ResultCallStructuralInputRef, error) {
	if frame == nil {
		return "", nil, nil
	}

	field, err := resultCallIdentityField(frame)
	if err != nil {
		return "", nil, err
	}
	if frame.Type == nil {
		return "", nil, fmt.Errorf("result call frame %q: missing type", field)
	}

	var inputRefs []ResultCallStructuralInputRef
	h := hashutil.NewHasher()

	if frame.Receiver != nil {
		inputRefs = append(inputRefs, ResultCallStructuralInputRef{
			Result: frame.Receiver,
		})
	}
	h = h.WithDelim()

	h = appendResultCallTypeBytes(h, frame.Type).
		WithDelim()

	h = h.WithString(field).
		WithDelim()

	for _, arg := range frame.Args {
		arg = redactedCallArgForDigest(arg)
		if arg == nil {
			continue
		}
		nextH, nextInputRefs, err := appendResultCallArgSelfRefs(c, arg, h, inputRefs)
		if err != nil {
			if h != nil {
				h.Close()
			}
			return "", nil, fmt.Errorf("result call frame %q args: %w", field, err)
		}
		h = nextH
		inputRefs = nextInputRefs
		h = h.WithDelim()
	}
	h = h.WithDelim()

	for _, input := range frame.ImplicitInputs {
		input = redactedCallArgForDigest(input)
		if input == nil {
			continue
		}
		nextH, nextInputRefs, err := appendResultCallArgSelfRefs(c, input, h, inputRefs)
		if err != nil {
			if h != nil {
				h.Close()
			}
			return "", nil, fmt.Errorf("result call frame %q implicit inputs: %w", field, err)
		}
		h = nextH
		inputRefs = nextInputRefs
		h = h.WithDelim()
	}

	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			if h != nil {
				h.Close()
			}
			return "", nil, fmt.Errorf("result call frame %q module: missing result ref", field)
		}
		inputRefs = append(inputRefs, ResultCallStructuralInputRef{
			Result: frame.Module.ResultRef,
		})
	}
	h = h.WithDelim()

	h = h.WithInt64(frame.Nth).
		WithDelim()

	h = h.WithString(frame.View.String()).
		WithDelim()

	for _, ref := range inputRefs {
		if err := ref.Validate(); err != nil {
			if h != nil {
				h.Close()
			}
			return "", nil, fmt.Errorf("result call frame %q structural inputs: %w", field, err)
		}
	}

	return digest.Digest(h.DigestAndClose()), inputRefs, nil
}

func (ref ResultCallStructuralInputRef) Validate() error {
	switch {
	case ref.Result != nil && ref.Digest != "":
		return fmt.Errorf("structural input ref cannot have both result and digest")
	case ref.Result != nil:
		return ref.Result.Validate()
	case ref.Result == nil && ref.Digest == "":
		return fmt.Errorf("structural input ref must have either result or digest")
	default:
		return nil
	}
}

func (ref ResultCallStructuralInputRef) InputDigest(ctx context.Context) (digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return ref.inputDigest(c)
}

func (ref ResultCallStructuralInputRef) inputDigest(c *Cache) (digest.Digest, error) {
	switch {
	case ref.Result != nil && ref.Digest != "":
		return "", fmt.Errorf("structural input ref cannot have both result and digest")
	case ref.Result != nil:
		return recipeDigestForResultCallRef(c, ref.Result, map[sharedResultID]struct{}{})
	case ref.Digest != "":
		return ref.Digest, nil
	default:
		return "", fmt.Errorf("structural input ref must have either result or digest")
	}
}

func (ref *ResultCallRef) clone() *ResultCallRef {
	if ref == nil {
		return nil
	}
	return &ResultCallRef{
		ResultID: ref.ResultID,
		Call:     ref.Call.clone(),
		shared:   ref.shared,
	}
}

func (ref *ResultCallRef) loadSharedCall() *ResultCall {
	if ref == nil || ref.shared == nil {
		return nil
	}
	return ref.shared.loadResultCall()
}

func (ref *ResultCallRef) Validate() error {
	if ref == nil {
		return fmt.Errorf("missing result ref")
	}
	switch {
	case ref.ResultID != 0 && ref.Call != nil:
		return fmt.Errorf("result ref cannot have both result ID and call")
	case ref.ResultID == 0 && ref.Call == nil:
		return fmt.Errorf("missing result ref")
	default:
		return nil
	}
}

func resultCallIdentityField(frame *ResultCall) (string, error) {
	if frame == nil {
		return "", fmt.Errorf("missing result call frame")
	}
	switch frame.Kind {
	case ResultCallKindSynthetic:
		if frame.SyntheticOp == "" {
			return "", fmt.Errorf("synthetic result call frame missing synthetic op")
		}
		return frame.SyntheticOp, nil
	default:
		if frame.Field == "" {
			return "", fmt.Errorf("field result call frame missing field")
		}
		return frame.Field, nil
	}
}

func recipeDigestForCachedResult(c *Cache, resultID sharedResultID, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if resultID == 0 {
		return "", fmt.Errorf("missing result ref")
	}
	if c == nil {
		return "", fmt.Errorf("cannot resolve result ref %d without cache", resultID)
	}
	if _, seen := visiting[resultID]; seen {
		return "", fmt.Errorf("cycle while reconstructing recipe digest for shared result %d", resultID)
	}
	c.egraphMu.RLock()
	res := c.resultsByID[resultID]
	c.egraphMu.RUnlock()
	if res == nil {
		return "", fmt.Errorf("missing shared result %d", resultID)
	}
	frame := res.loadResultCall()
	if frame == nil {
		return "", fmt.Errorf("missing result call frame for shared result %d", resultID)
	}

	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	return frame.recipeDigestWithVisiting(c, visiting)
}

func recipeDigestForResultCallRef(c *Cache, ref *ResultCallRef, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	if ref.Call != nil {
		return ref.Call.recipeDigestWithVisiting(c, visiting)
	}
	if frame := ref.loadSharedCall(); frame != nil {
		return frame.recipeDigestWithVisiting(c, visiting)
	}
	return recipeDigestForCachedResult(c, sharedResultID(ref.ResultID), visiting)
}

func contentPreferredDigestForCachedResult(c *Cache, resultID sharedResultID, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if resultID == 0 {
		return "", fmt.Errorf("missing result ref")
	}
	if c == nil {
		return "", fmt.Errorf("cannot resolve result ref %d without cache", resultID)
	}
	if _, seen := visiting[resultID]; seen {
		return "", fmt.Errorf("cycle while reconstructing content-preferred digest for shared result %d", resultID)
	}
	c.egraphMu.RLock()
	res := c.resultsByID[resultID]
	c.egraphMu.RUnlock()
	if res == nil {
		return "", fmt.Errorf("missing shared result %d", resultID)
	}
	frame := res.loadResultCall()
	if frame == nil {
		return "", fmt.Errorf("missing result call frame for shared result %d", resultID)
	}

	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	return frame.contentPreferredDigestWithVisiting(c, visiting)
}

func contentPreferredDigestForResultCallRef(c *Cache, ref *ResultCallRef, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	if ref.Call != nil {
		return ref.Call.contentPreferredDigestWithVisiting(c, visiting)
	}
	if frame := ref.loadSharedCall(); frame != nil {
		return frame.contentPreferredDigestWithVisiting(c, visiting)
	}
	return contentPreferredDigestForCachedResult(c, sharedResultID(ref.ResultID), visiting)
}

func appendResultCallTypeBytes(h *hashutil.Hasher, typ *ResultCallType) *hashutil.Hasher {
	for curType := typ; curType != nil; curType = curType.Elem {
		h = h.WithString(curType.NamedType)
		if curType.NonNull {
			h = h.WithByte(2)
		} else {
			h = h.WithByte(1)
		}
		h = h.WithDelim()
	}
	return h
}

func redactedCallArgForDigest(arg *ResultCallArg) *ResultCallArg {
	if arg == nil {
		return nil
	}
	if !arg.IsSensitive {
		return arg
	}
	return &ResultCallArg{
		Name:  arg.Name,
		Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "***"},
	}
}

func appendResultCallArgBytes(
	c *Cache,
	arg *ResultCallArg,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	h = h.WithString(arg.Name)
	nextH, err := appendResultCallLiteralBytes(c, arg.Value, h, visiting)
	if err != nil {
		return h, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}
	return nextH, nil
}

func appendResultCallArgContentPreferredBytes(
	c *Cache,
	arg *ResultCallArg,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	h = h.WithString(arg.Name)
	nextH, err := appendResultCallLiteralContentPreferredBytes(c, arg.Value, h, visiting)
	if err != nil {
		return h, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}
	return nextH, nil
}

func appendResultCallArgSelfRefs(
	c *Cache,
	arg *ResultCallArg,
	h *hashutil.Hasher,
	inputs []ResultCallStructuralInputRef,
) (*hashutil.Hasher, []ResultCallStructuralInputRef, error) {
	h = h.WithString(arg.Name)
	nextH, nextInputs, err := appendResultCallLiteralSelfRefs(c, arg.Value, h, inputs)
	if err != nil {
		return h, inputs, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}
	return nextH, nextInputs, nil
}

func appendResultCallLiteralBytes(
	c *Cache,
	lit *ResultCallLiteral,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	var err error
	switch {
	case lit == nil || lit.Kind == ResultCallLiteralKindNull:
		const prefix = '1'
		h = h.WithByte(prefix).WithByte(1)
	case lit.Kind == ResultCallLiteralKindResultRef:
		const prefix = '0'
		dig, err := recipeDigestForResultCallRef(c, lit.ResultRef, visiting)
		if err != nil {
			return nil, fmt.Errorf("result ref digest: %w", err)
		}
		h = h.WithByte(prefix).WithString(dig.String())
	case lit.Kind == ResultCallLiteralKindBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if lit.BoolValue {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case lit.Kind == ResultCallLiteralKindEnum:
		const prefix = '3'
		h = h.WithByte(prefix).WithString(lit.EnumValue)
	case lit.Kind == ResultCallLiteralKindInt:
		const prefix = '4'
		h = h.WithByte(prefix).WithInt64(lit.IntValue)
	case lit.Kind == ResultCallLiteralKindFloat:
		const prefix = '5'
		h = h.WithByte(prefix).WithFloat64(lit.FloatValue)
	case lit.Kind == ResultCallLiteralKindString:
		const prefix = '6'
		h = h.WithByte(prefix).WithString(lit.StringValue)
	case lit.Kind == ResultCallLiteralKindList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range lit.ListItems {
			h, err = appendResultCallLiteralBytes(c, elem, h, visiting)
			if err != nil {
				return nil, err
			}
		}
	case lit.Kind == ResultCallLiteralKindObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, field := range lit.ObjectFields {
			h, err = appendResultCallArgBytes(c, field, h, visiting)
			if err != nil {
				return nil, err
			}
			h = h.WithDelim()
		}
	case lit.Kind == ResultCallLiteralKindDigestedString:
		const prefix = '9'
		h = h.WithByte(prefix)
		if lit.DigestedStringDigest != "" {
			h = h.WithString(lit.DigestedStringDigest.String())
		}
	default:
		return nil, fmt.Errorf("unknown result call frame literal kind %q", lit.Kind)
	}
	h = h.WithDelim()
	return h, nil
}

func appendResultCallLiteralContentPreferredBytes(
	c *Cache,
	lit *ResultCallLiteral,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	var err error
	switch {
	case lit == nil || lit.Kind == ResultCallLiteralKindNull:
		const prefix = '1'
		h = h.WithByte(prefix).WithByte(1)
	case lit.Kind == ResultCallLiteralKindResultRef:
		const prefix = '0'
		dig, err := contentPreferredDigestForResultCallRef(c, lit.ResultRef, visiting)
		if err != nil {
			return nil, fmt.Errorf("result ref content-preferred digest: %w", err)
		}
		h = h.WithByte(prefix).WithString(dig.String())
	case lit.Kind == ResultCallLiteralKindBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if lit.BoolValue {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case lit.Kind == ResultCallLiteralKindEnum:
		const prefix = '3'
		h = h.WithByte(prefix).WithString(lit.EnumValue)
	case lit.Kind == ResultCallLiteralKindInt:
		const prefix = '4'
		h = h.WithByte(prefix).WithInt64(lit.IntValue)
	case lit.Kind == ResultCallLiteralKindFloat:
		const prefix = '5'
		h = h.WithByte(prefix).WithFloat64(lit.FloatValue)
	case lit.Kind == ResultCallLiteralKindString:
		const prefix = '6'
		h = h.WithByte(prefix).WithString(lit.StringValue)
	case lit.Kind == ResultCallLiteralKindList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range lit.ListItems {
			h, err = appendResultCallLiteralContentPreferredBytes(c, elem, h, visiting)
			if err != nil {
				return nil, err
			}
		}
	case lit.Kind == ResultCallLiteralKindObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, field := range lit.ObjectFields {
			h, err = appendResultCallArgContentPreferredBytes(c, field, h, visiting)
			if err != nil {
				return nil, err
			}
			h = h.WithDelim()
		}
	case lit.Kind == ResultCallLiteralKindDigestedString:
		const prefix = '9'
		h = h.WithByte(prefix)
		if lit.DigestedStringDigest != "" {
			h = h.WithString(lit.DigestedStringDigest.String())
		}
	default:
		return nil, fmt.Errorf("unknown result call frame literal kind %q", lit.Kind)
	}
	h = h.WithDelim()
	return h, nil
}

func appendResultCallLiteralSelfRefs(
	c *Cache,
	lit *ResultCallLiteral,
	h *hashutil.Hasher,
	inputs []ResultCallStructuralInputRef,
) (*hashutil.Hasher, []ResultCallStructuralInputRef, error) {
	var err error
	switch {
	case lit == nil || lit.Kind == ResultCallLiteralKindNull:
		const prefix = '1'
		h = h.WithByte(prefix).WithByte(1)
	case lit.Kind == ResultCallLiteralKindResultRef:
		const prefix = '0'
		h = h.WithByte(prefix)
		inputs = append(inputs, ResultCallStructuralInputRef{
			Result: lit.ResultRef,
		})
	case lit.Kind == ResultCallLiteralKindBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if lit.BoolValue {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case lit.Kind == ResultCallLiteralKindEnum:
		const prefix = '3'
		h = h.WithByte(prefix).WithString(lit.EnumValue)
	case lit.Kind == ResultCallLiteralKindInt:
		const prefix = '4'
		h = h.WithByte(prefix).WithInt64(lit.IntValue)
	case lit.Kind == ResultCallLiteralKindFloat:
		const prefix = '5'
		h = h.WithByte(prefix).WithFloat64(lit.FloatValue)
	case lit.Kind == ResultCallLiteralKindString:
		const prefix = '6'
		h = h.WithByte(prefix).WithString(lit.StringValue)
	case lit.Kind == ResultCallLiteralKindList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range lit.ListItems {
			h, inputs, err = appendResultCallLiteralSelfRefs(c, elem, h, inputs)
			if err != nil {
				return nil, nil, err
			}
		}
	case lit.Kind == ResultCallLiteralKindObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, field := range lit.ObjectFields {
			h, inputs, err = appendResultCallArgSelfRefs(c, field, h, inputs)
			if err != nil {
				return nil, nil, err
			}
			h = h.WithDelim()
		}
	case lit.Kind == ResultCallLiteralKindDigestedString:
		const prefix = '9'
		h = h.WithByte(prefix)
		if lit.DigestedStringDigest != "" {
			inputs = append(inputs, ResultCallStructuralInputRef{Digest: lit.DigestedStringDigest})
		}
	default:
		return nil, nil, fmt.Errorf("unknown result call frame literal kind %q", lit.Kind)
	}
	h = h.WithDelim()
	return h, inputs, nil
}

func (c *Cache) RecipeIDForCall(ctx context.Context, frame *ResultCall) (*call.ID, error) {
	if frame == nil {
		return nil, fmt.Errorf("rebuild recipe ID: nil call")
	}
	return frame.recipeIDWithContext(ctx, c)
}

func (c *Cache) RecipeDigestForCall(frame *ResultCall) (digest.Digest, error) {
	if frame == nil {
		return "", fmt.Errorf("derive recipe digest: nil call")
	}
	return frame.deriveRecipeDigest(c)
}

// resultCallByResultID returns the cache-owned immutable result-call frame for
// the given shared result. Callers must treat the returned frame as read-only.
func (c *Cache) resultCallByResultID(resultID sharedResultID) *ResultCall {
	if resultID == 0 {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[resultID]
	if res == nil {
		return nil
	}
	return res.loadResultCall()
}

func (frame *ResultCall) recipeIDWithVisiting(ctx context.Context, c *Cache, visiting map[sharedResultID]struct{}) (*call.ID, error) {
	if frame == nil {
		return nil, fmt.Errorf("rebuild recipe ID: nil frame")
	}
	field := frame.Field
	if frame.Kind == ResultCallKindSynthetic {
		field = frame.SyntheticOp
	}
	if field == "" {
		return nil, fmt.Errorf("rebuild recipe ID: missing field")
	}

	var (
		receiverID *call.ID
		mod        *call.Module
	)
	if frame.Receiver != nil {
		id, err := frame.resolveRefRecipeID(ctx, c, frame.Receiver, visiting)
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
		receiverID = id
	}
	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			return nil, fmt.Errorf("module: missing result ref")
		}
		modID, err := frame.resolveRefRecipeID(ctx, c, frame.Module.ResultRef, visiting)
		if err != nil {
			return nil, fmt.Errorf("module: %w", err)
		}
		mod = call.NewModule(modID, frame.Module.Name, frame.Module.Ref, frame.Module.Pin)
	}

	args, err := frame.callArgs(ctx, c, frame.Args, visiting)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	implicitInputs, err := frame.callArgs(ctx, c, frame.ImplicitInputs, visiting)
	if err != nil {
		return nil, fmt.Errorf("implicit inputs: %w", err)
	}

	rebuilt := receiverID
	rebuilt = rebuilt.Append(
		frame.Type.toAST(),
		field,
		call.WithView(frame.View),
		call.WithNth(int(frame.Nth)),
		call.WithEffectIDs(frame.EffectIDs),
		call.WithArgs(args...),
		call.WithImplicitInputs(implicitInputs...),
		call.WithModule(mod),
	)
	if rebuilt == nil {
		return nil, fmt.Errorf("rebuild recipe ID: append returned nil")
	}
	for _, extra := range frame.ExtraDigests {
		if extra.Digest == "" {
			continue
		}
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt, nil
}

func (frame *ResultCall) resolveRefRecipeID(ctx context.Context, c *Cache, ref *ResultCallRef, visiting map[sharedResultID]struct{}) (*call.ID, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Call != nil {
		return ref.Call.recipeIDWithVisiting(ctx, c, visiting)
	}
	if c == nil {
		return nil, fmt.Errorf("cannot resolve result ref %d without cache", ref.ResultID)
	}
	refID := sharedResultID(ref.ResultID)
	refFrame := ref.loadSharedCall()
	if refFrame == nil {
		refFrame = c.resultCallByResultID(refID)
	}
	if refFrame == nil {
		c.traceRecipeIDRebuildFailed(ctx, frame, ref, "missing_result_call_frame")
		return nil, fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
	}
	if _, seen := visiting[refID]; seen {
		return nil, fmt.Errorf("cycle while reconstructing recipe ID for shared result %d", ref.ResultID)
	}
	visiting[refID] = struct{}{}
	defer delete(visiting, refID)
	return refFrame.recipeIDWithVisiting(ctx, c, visiting)
}

func (frame *ResultCall) callArgs(
	ctx context.Context,
	c *Cache,
	frameArgs []*ResultCallArg,
	visiting map[sharedResultID]struct{},
) ([]*call.Argument, error) {
	if len(frameArgs) == 0 {
		return nil, nil
	}
	args := make([]*call.Argument, 0, len(frameArgs))
	for _, frameArg := range frameArgs {
		if frameArg == nil || frameArg.Value == nil {
			continue
		}
		lit, err := frame.callLiteral(ctx, c, frameArg.Value, visiting)
		if err != nil {
			return nil, err
		}
		args = append(args, call.NewArgument(frameArg.Name, lit, frameArg.IsSensitive))
	}
	return args, nil
}

func (frame *ResultCall) callLiteral(
	ctx context.Context,
	c *Cache,
	frameLit *ResultCallLiteral,
	visiting map[sharedResultID]struct{},
) (call.Literal, error) {
	if frameLit == nil {
		return nil, fmt.Errorf("missing literal")
	}
	switch frameLit.Kind {
	case ResultCallLiteralKindNull:
		return call.NewLiteralNull(), nil
	case ResultCallLiteralKindBool:
		return call.NewLiteralBool(frameLit.BoolValue), nil
	case ResultCallLiteralKindInt:
		return call.NewLiteralInt(frameLit.IntValue), nil
	case ResultCallLiteralKindFloat:
		return call.NewLiteralFloat(frameLit.FloatValue), nil
	case ResultCallLiteralKindString:
		return call.NewLiteralString(frameLit.StringValue), nil
	case ResultCallLiteralKindEnum:
		return call.NewLiteralEnum(frameLit.EnumValue), nil
	case ResultCallLiteralKindDigestedString:
		return call.NewLiteralDigestedString(frameLit.DigestedStringValue, frameLit.DigestedStringDigest), nil
	case ResultCallLiteralKindResultRef:
		id, err := frame.resolveRefRecipeID(ctx, c, frameLit.ResultRef, visiting)
		if err != nil {
			return nil, err
		}
		return call.NewLiteralID(id), nil
	case ResultCallLiteralKindList:
		items := make([]call.Literal, 0, len(frameLit.ListItems))
		for _, item := range frameLit.ListItems {
			lit, err := frame.callLiteral(ctx, c, item, visiting)
			if err != nil {
				return nil, err
			}
			items = append(items, lit)
		}
		return call.NewLiteralList(items...), nil
	case ResultCallLiteralKindObject:
		fields := make([]*call.Argument, 0, len(frameLit.ObjectFields))
		for _, field := range frameLit.ObjectFields {
			if field == nil || field.Value == nil {
				continue
			}
			lit, err := frame.callLiteral(ctx, c, field.Value, visiting)
			if err != nil {
				return nil, err
			}
			fields = append(fields, call.NewArgument(field.Name, lit, field.IsSensitive))
		}
		return call.NewLiteralObject(fields...), nil
	default:
		return nil, fmt.Errorf("unknown result call frame literal kind %q", frameLit.Kind)
	}
}
