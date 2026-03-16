package dagql

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
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

func (typ *ResultCallFrameType) toAST() *ast.Type {
	if typ == nil {
		return nil
	}
	return &ast.Type{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      typ.Elem.toAST(),
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
	ResultCallFrameLiteralKindNull           ResultCallFrameLiteralKind = "null"
	ResultCallFrameLiteralKindBool           ResultCallFrameLiteralKind = "bool"
	ResultCallFrameLiteralKindInt            ResultCallFrameLiteralKind = "int"
	ResultCallFrameLiteralKindFloat          ResultCallFrameLiteralKind = "float"
	ResultCallFrameLiteralKindString         ResultCallFrameLiteralKind = "string"
	ResultCallFrameLiteralKindEnum           ResultCallFrameLiteralKind = "enum"
	ResultCallFrameLiteralKindDigestedString ResultCallFrameLiteralKind = "digested_string"
	ResultCallFrameLiteralKindResultRef      ResultCallFrameLiteralKind = "result_ref"
	ResultCallFrameLiteralKindList           ResultCallFrameLiteralKind = "list"
	ResultCallFrameLiteralKindObject         ResultCallFrameLiteralKind = "object"
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

	ResultRef    *ResultCallFrameRef       `json:"resultRef,omitempty"`
	ListItems    []*ResultCallFrameLiteral `json:"listItems,omitempty"`
	ObjectFields []*ResultCallFrameArg     `json:"objectFields,omitempty"`
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
	Kind        ResultCallFrameKind  `json:"kind"`
	Type        *ResultCallFrameType `json:"type,omitempty"`
	Field       string               `json:"field,omitempty"`
	SyntheticOp string               `json:"syntheticOp,omitempty"`
	View        call.View            `json:"view,omitempty"`
	Nth         int64                `json:"nth,omitempty"`
	EffectIDs   []string             `json:"effectIDs,omitempty"`
	// ExtraDigests are the original extra digests explicitly attached when this
	// call/result was first created. They are useful provenance, but they are
	// not the authoritative merged digest state. The cache/e-graph remains the
	// source of truth for the full merged output-equivalence digest set.
	ExtraDigests   []call.ExtraDigest     `json:"extraDigests,omitempty"`
	Receiver       *ResultCallFrameRef    `json:"receiver,omitempty"`
	Module         *ResultCallFrameModule `json:"module,omitempty"`
	Args           []*ResultCallFrameArg  `json:"args,omitempty"`
	ImplicitInputs []*ResultCallFrameArg  `json:"implicitInputs,omitempty"`

	// cache is a runtime-only backpointer used for recursive digest resolution
	// through ResultCallFrameRef.ResultID. It is not persisted.
	cache *cache

	// recipeDigest is memoized once the frame has reached its finalized
	// semantic shape. Do not mutate the frame after calling RecipeDigest.
	recipeDigestOnce sync.Once
	recipeDigestErr  error
	recipeDigest     digest.Digest
}

type ResultCallFrameStructuralInputRef struct {
	Result *ResultCallFrameRef
	Digest digest.Digest

	cache *cache
}

func (frame *ResultCallFrame) clone() *ResultCallFrame {
	if frame == nil {
		return nil
	}
	cp := &ResultCallFrame{
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
		cache:        frame.cache,
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

func (frame *ResultCallFrame) RecipeDigest() (digest.Digest, error) {
	return frame.recipeDigestWithVisiting(map[sharedResultID]struct{}{})
}

func (frame *ResultCallFrame) recipeDigestWithVisiting(visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}

	frame.recipeDigestOnce.Do(func() {
		field, err := resultCallFrameIdentityField(frame)
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
			receiverDigest, err := recipeDigestForFrameRef(frame.cache, frame.Receiver, visiting)
			if err != nil {
				h.Close()
				frame.recipeDigestErr = fmt.Errorf("receiver: %w", err)
				return
			}
			h = h.WithString(receiverDigest.String())
		}
		h = h.WithDelim()

		h = appendResultCallFrameTypeBytes(h, frame.Type).
			WithDelim()

		h = h.WithString(field).
			WithDelim()

		for _, arg := range frame.Args {
			arg = redactedFrameArgForDigest(arg)
			if arg == nil {
				continue
			}
			h, err = appendResultCallFrameArgBytes(frame.cache, arg, h, visiting)
			if err != nil {
				h.Close()
				frame.recipeDigestErr = fmt.Errorf("args: %w", err)
				return
			}
			h = h.WithDelim()
		}
		h = h.WithDelim()

		for _, input := range frame.ImplicitInputs {
			input = redactedFrameArgForDigest(input)
			if input == nil {
				continue
			}
			h, err = appendResultCallFrameArgBytes(frame.cache, input, h, visiting)
			if err != nil {
				h.Close()
				frame.recipeDigestErr = fmt.Errorf("implicit inputs: %w", err)
				return
			}
			h = h.WithDelim()
		}
		h = h.WithDelim()

		if frame.Module != nil && frame.Module.ResultRef != nil {
			moduleDigest, err := recipeDigestForFrameRef(frame.cache, frame.Module.ResultRef, visiting)
			if err != nil {
				h.Close()
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

func (frame *ResultCallFrame) SelfDigestAndInputRefs() (digest.Digest, []ResultCallFrameStructuralInputRef, error) {
	if frame == nil {
		return "", nil, nil
	}

	field, err := resultCallFrameIdentityField(frame)
	if err != nil {
		return "", nil, err
	}
	if frame.Type == nil {
		return "", nil, fmt.Errorf("result call frame %q: missing type", field)
	}

	var inputRefs []ResultCallFrameStructuralInputRef
	h := hashutil.NewHasher()

	if frame.Receiver != nil {
		inputRefs = append(inputRefs, ResultCallFrameStructuralInputRef{
			Result: frame.Receiver,
			cache:  frame.cache,
		})
	}
	h = h.WithDelim()

	h = appendResultCallFrameTypeBytes(h, frame.Type).
		WithDelim()

	h = h.WithString(field).
		WithDelim()

	for _, arg := range frame.Args {
		arg = redactedFrameArgForDigest(arg)
		if arg == nil {
			continue
		}
		h, inputRefs, err = appendResultCallFrameArgSelfRefs(arg, h, inputRefs)
		if err != nil {
			h.Close()
			return "", nil, fmt.Errorf("result call frame %q args: %w", field, err)
		}
		h = h.WithDelim()
	}
	h = h.WithDelim()

	for _, input := range frame.ImplicitInputs {
		input = redactedFrameArgForDigest(input)
		if input == nil {
			continue
		}
		h, inputRefs, err = appendResultCallFrameArgSelfRefs(input, h, inputRefs)
		if err != nil {
			h.Close()
			return "", nil, fmt.Errorf("result call frame %q implicit inputs: %w", field, err)
		}
		h = h.WithDelim()
	}

	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			h.Close()
			return "", nil, fmt.Errorf("result call frame %q module: missing result ref", field)
		}
		inputRefs = append(inputRefs, ResultCallFrameStructuralInputRef{
			Result: frame.Module.ResultRef,
			cache:  frame.cache,
		})
	}
	h = h.WithDelim()

	h = h.WithInt64(frame.Nth).
		WithDelim()

	h = h.WithString(frame.View.String()).
		WithDelim()

	for _, ref := range inputRefs {
		if err := ref.Validate(); err != nil {
			h.Close()
			return "", nil, fmt.Errorf("result call frame %q structural inputs: %w", field, err)
		}
	}

	return digest.Digest(h.DigestAndClose()), inputRefs, nil
}

func (frame *ResultCallFrame) AllEffectIDs() ([]string, error) {
	if frame == nil {
		return nil, nil
	}
	seenResults := map[sharedResultID]struct{}{}
	seenEffects := map[string]struct{}{}
	var out []string

	var walkFrame func(*ResultCallFrame) error
	var walkRef func(*ResultCallFrameRef) error
	var walkLiteral func(*ResultCallFrameLiteral) error

	walkRef = func(ref *ResultCallFrameRef) error {
		if ref == nil || ref.ResultID == 0 {
			return nil
		}
		resultID := sharedResultID(ref.ResultID)
		if _, seen := seenResults[resultID]; seen {
			return nil
		}
		seenResults[resultID] = struct{}{}
		if frame.cache == nil {
			return fmt.Errorf("cannot resolve effect IDs for result ref %d without cache", ref.ResultID)
		}
		target := frame.cache.resultCallFrameByResultID(resultID)
		if target == nil {
			return fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
		}
		return walkFrame(target)
	}

	walkLiteral = func(lit *ResultCallFrameLiteral) error {
		if lit == nil {
			return nil
		}
		switch lit.Kind {
		case ResultCallFrameLiteralKindResultRef:
			return walkRef(lit.ResultRef)
		case ResultCallFrameLiteralKindList:
			for _, item := range lit.ListItems {
				if err := walkLiteral(item); err != nil {
					return err
				}
			}
		case ResultCallFrameLiteralKindObject:
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

	walkFrame = func(cur *ResultCallFrame) error {
		if cur == nil {
			return nil
		}
		for _, effect := range cur.EffectIDs {
			if _, seen := seenEffects[effect]; seen {
				continue
			}
			seenEffects[effect] = struct{}{}
			out = append(out, effect)
		}
		if err := walkRef(cur.Receiver); err != nil {
			return err
		}
		if cur.Module != nil {
			if err := walkRef(cur.Module.ResultRef); err != nil {
				return err
			}
		}
		for _, arg := range cur.Args {
			if arg == nil {
				continue
			}
			if err := walkLiteral(arg.Value); err != nil {
				return err
			}
		}
		for _, input := range cur.ImplicitInputs {
			if input == nil {
				continue
			}
			if err := walkLiteral(input.Value); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walkFrame(frame); err != nil {
		return nil, err
	}
	return out, nil
}

func (frame *ResultCallFrame) bindCache(c *cache) {
	if frame == nil {
		return
	}
	frame.cache = c
}

func (ref ResultCallFrameStructuralInputRef) Validate() error {
	switch {
	case ref.Result != nil && ref.Digest != "":
		return fmt.Errorf("structural input ref cannot have both result and digest")
	case ref.Result == nil && ref.Digest == "":
		return fmt.Errorf("structural input ref must have either result or digest")
	default:
		return nil
	}
}

func (ref ResultCallFrameStructuralInputRef) InputDigest() (digest.Digest, error) {
	switch {
	case ref.Result != nil && ref.Digest != "":
		return "", fmt.Errorf("structural input ref cannot have both result and digest")
	case ref.Result != nil:
		return recipeDigestForFrameRef(ref.cache, ref.Result, map[sharedResultID]struct{}{})
	case ref.Digest != "":
		return ref.Digest, nil
	default:
		return "", fmt.Errorf("structural input ref must have either result or digest")
	}
}

func (ref *ResultCallFrameRef) clone() *ResultCallFrameRef {
	if ref == nil {
		return nil
	}
	return &ResultCallFrameRef{ResultID: ref.ResultID}
}

func resultCallFrameIdentityField(frame *ResultCallFrame) (string, error) {
	if frame == nil {
		return "", fmt.Errorf("missing result call frame")
	}
	switch frame.Kind {
	case ResultCallFrameKindSynthetic:
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

func recipeDigestForCachedResult(c *cache, resultID sharedResultID, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if resultID == 0 {
		return "", fmt.Errorf("missing result ref")
	}
	if c == nil {
		return "", fmt.Errorf("cannot resolve result ref %d without cache", resultID)
	}
	if _, seen := visiting[resultID]; seen {
		return "", fmt.Errorf("cycle while reconstructing recipe digest for shared result %d", resultID)
	}
	frame := c.resultCallFrameByResultID(resultID)
	if frame == nil {
		return "", fmt.Errorf("missing result call frame for shared result %d", resultID)
	}

	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	return frame.recipeDigestWithVisiting(visiting)
}

func recipeDigestForFrameRef(c *cache, ref *ResultCallFrameRef, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if ref == nil || ref.ResultID == 0 {
		return "", fmt.Errorf("missing result ref")
	}
	return recipeDigestForCachedResult(c, sharedResultID(ref.ResultID), visiting)
}

func appendResultCallFrameTypeBytes(h *hashutil.Hasher, typ *ResultCallFrameType) *hashutil.Hasher {
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

func redactedFrameArgForDigest(arg *ResultCallFrameArg) *ResultCallFrameArg {
	if arg == nil {
		return nil
	}
	if !arg.IsSensitive {
		return arg
	}
	return &ResultCallFrameArg{
		Name:  arg.Name,
		Value: &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindString, StringValue: "***"},
	}
}

func appendResultCallFrameArgBytes(
	c *cache,
	arg *ResultCallFrameArg,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	h = h.WithString(arg.Name)
	h, err := appendResultCallFrameLiteralBytes(c, arg.Value, h, visiting)
	if err != nil {
		return nil, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}
	return h, nil
}

func appendResultCallFrameArgSelfRefs(
	arg *ResultCallFrameArg,
	h *hashutil.Hasher,
	inputs []ResultCallFrameStructuralInputRef,
) (*hashutil.Hasher, []ResultCallFrameStructuralInputRef, error) {
	h = h.WithString(arg.Name)
	h, inputs, err := appendResultCallFrameLiteralSelfRefs(arg.Value, h, inputs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}
	return h, inputs, nil
}

func appendResultCallFrameLiteralBytes(
	c *cache,
	lit *ResultCallFrameLiteral,
	h *hashutil.Hasher,
	visiting map[sharedResultID]struct{},
) (*hashutil.Hasher, error) {
	var err error
	switch {
	case lit == nil || lit.Kind == ResultCallFrameLiteralKindNull:
		const prefix = '1'
		h = h.WithByte(prefix).WithByte(1)
	case lit.Kind == ResultCallFrameLiteralKindResultRef:
		const prefix = '0'
		dig, err := recipeDigestForFrameRef(c, lit.ResultRef, visiting)
		if err != nil {
			return nil, fmt.Errorf("result ref digest: %w", err)
		}
		h = h.WithByte(prefix).WithString(dig.String())
	case lit.Kind == ResultCallFrameLiteralKindBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if lit.BoolValue {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case lit.Kind == ResultCallFrameLiteralKindEnum:
		const prefix = '3'
		h = h.WithByte(prefix).WithString(lit.EnumValue)
	case lit.Kind == ResultCallFrameLiteralKindInt:
		const prefix = '4'
		h = h.WithByte(prefix).WithInt64(lit.IntValue)
	case lit.Kind == ResultCallFrameLiteralKindFloat:
		const prefix = '5'
		h = h.WithByte(prefix).WithFloat64(lit.FloatValue)
	case lit.Kind == ResultCallFrameLiteralKindString:
		const prefix = '6'
		h = h.WithByte(prefix).WithString(lit.StringValue)
	case lit.Kind == ResultCallFrameLiteralKindList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range lit.ListItems {
			h, err = appendResultCallFrameLiteralBytes(c, elem, h, visiting)
			if err != nil {
				return nil, err
			}
		}
	case lit.Kind == ResultCallFrameLiteralKindObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, field := range lit.ObjectFields {
			h, err = appendResultCallFrameArgBytes(c, field, h, visiting)
			if err != nil {
				return nil, err
			}
			h = h.WithDelim()
		}
	case lit.Kind == ResultCallFrameLiteralKindDigestedString:
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

func appendResultCallFrameLiteralSelfRefs(
	lit *ResultCallFrameLiteral,
	h *hashutil.Hasher,
	inputs []ResultCallFrameStructuralInputRef,
) (*hashutil.Hasher, []ResultCallFrameStructuralInputRef, error) {
	var err error
	switch {
	case lit == nil || lit.Kind == ResultCallFrameLiteralKindNull:
		const prefix = '1'
		h = h.WithByte(prefix).WithByte(1)
	case lit.Kind == ResultCallFrameLiteralKindResultRef:
		const prefix = '0'
		h = h.WithByte(prefix)
		inputs = append(inputs, ResultCallFrameStructuralInputRef{
			Result: lit.ResultRef,
		})
	case lit.Kind == ResultCallFrameLiteralKindBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if lit.BoolValue {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case lit.Kind == ResultCallFrameLiteralKindEnum:
		const prefix = '3'
		h = h.WithByte(prefix).WithString(lit.EnumValue)
	case lit.Kind == ResultCallFrameLiteralKindInt:
		const prefix = '4'
		h = h.WithByte(prefix).WithInt64(lit.IntValue)
	case lit.Kind == ResultCallFrameLiteralKindFloat:
		const prefix = '5'
		h = h.WithByte(prefix).WithFloat64(lit.FloatValue)
	case lit.Kind == ResultCallFrameLiteralKindString:
		const prefix = '6'
		h = h.WithByte(prefix).WithString(lit.StringValue)
	case lit.Kind == ResultCallFrameLiteralKindList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range lit.ListItems {
			h, inputs, err = appendResultCallFrameLiteralSelfRefs(elem, h, inputs)
			if err != nil {
				return nil, nil, err
			}
		}
	case lit.Kind == ResultCallFrameLiteralKindObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, field := range lit.ObjectFields {
			h, inputs, err = appendResultCallFrameArgSelfRefs(field, h, inputs)
			if err != nil {
				return nil, nil, err
			}
			h = h.WithDelim()
		}
	case lit.Kind == ResultCallFrameLiteralKindDigestedString:
		const prefix = '9'
		h = h.WithByte(prefix)
		if lit.DigestedStringDigest != "" {
			inputs = append(inputs, ResultCallFrameStructuralInputRef{Digest: lit.DigestedStringDigest})
		}
	default:
		return nil, nil, fmt.Errorf("unknown result call frame literal kind %q", lit.Kind)
	}
	h = h.WithDelim()
	return h, inputs, nil
}

func (c *cache) resultCallFrameForIDLocked(ctx context.Context, id *call.ID) (*ResultCallFrame, error) {
	if id == nil {
		return nil, nil
	}
	frame := &ResultCallFrame{
		Kind:         ResultCallFrameKindField,
		Type:         NewResultCallFrameType(id.Type().ToAST()),
		Field:        id.Field(),
		View:         id.View(),
		Nth:          id.Nth(),
		EffectIDs:    slices.Clone(id.EffectIDs()),
		ExtraDigests: slices.Clone(id.ExtraDigests()),
		cache:        c,
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

func (c *cache) resultCallFrameSnapshot(resultID sharedResultID) *ResultCallFrame {
	if resultID == 0 {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[resultID]
	if res == nil || res.resultCallFrame == nil {
		return nil
	}
	return res.resultCallFrame.clone()
}

func (c *cache) resultCallFrameByResultID(resultID sharedResultID) *ResultCallFrame {
	if resultID == 0 {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[resultID]
	if res == nil || res.resultCallFrame == nil {
		return nil
	}
	return res.resultCallFrame
}

func (c *cache) persistedCallIDByResultID(ctx context.Context, resultID sharedResultID) (*call.ID, error) {
	if resultID == 0 {
		return nil, fmt.Errorf("resolve persisted call ID: zero result ID")
	}
	frame := c.resultCallFrameSnapshot(resultID)
	if frame == nil {
		return nil, fmt.Errorf("resolve persisted call ID for result %d: missing result call frame", resultID)
	}
	rebuilt, ok := c.callIDFromFrame(ctx, frame, map[sharedResultID]struct{}{})
	if !ok || rebuilt == nil {
		return nil, fmt.Errorf("resolve persisted call ID for result %d: failed to rebuild from frame", resultID)
	}
	for _, extra := range c.resultCallFrameExtraDigestsSnapshot(resultID) {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt, nil
}

func (c *cache) resultCallFrameExtraDigestsSnapshot(resultID sharedResultID) []call.ExtraDigest {
	if resultID == 0 {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	outputEqClasses := c.outputEqClassesForResultLocked(resultID)
	if len(outputEqClasses) == 0 {
		return nil
	}

	seen := make(map[call.ExtraDigest]struct{})
	extras := make([]call.ExtraDigest, 0)
	if res := c.resultsByID[resultID]; res != nil && res.resultCallFrame != nil {
		for _, extra := range res.resultCallFrame.ExtraDigests {
			if extra.Digest == "" {
				continue
			}
			if _, ok := seen[extra]; ok {
				continue
			}
			seen[extra] = struct{}{}
			extras = append(extras, extra)
		}
	}
	for outputEqID := range outputEqClasses {
		for extra := range c.eqClassExtraDigests[outputEqID] {
			if extra.Digest == "" {
				continue
			}
			if _, ok := seen[extra]; ok {
				continue
			}
			seen[extra] = struct{}{}
			extras = append(extras, extra)
		}
	}
	sort.Slice(extras, func(i, j int) bool {
		if extras[i].Label != extras[j].Label {
			return extras[i].Label < extras[j].Label
		}
		return extras[i].Digest < extras[j].Digest
	})
	return extras
}

func (c *cache) callIDFromFrame(
	ctx context.Context,
	frame *ResultCallFrame,
	visiting map[sharedResultID]struct{},
) (*call.ID, bool) {
	if frame == nil {
		return nil, false
	}
	field := frame.Field
	if frame.Kind == ResultCallFrameKindSynthetic {
		field = frame.SyntheticOp
	}
	if field == "" {
		return nil, false
	}

	var (
		receiverID *call.ID
		mod        *call.Module
	)
	if frame.Receiver != nil {
		id, ok := c.resolveFrameRefCallID(ctx, frame.Receiver, visiting)
		if !ok {
			return nil, false
		}
		receiverID = id
	}
	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			return nil, false
		}
		modID, ok := c.resolveFrameRefCallID(ctx, frame.Module.ResultRef, visiting)
		if !ok || modID == nil {
			return nil, false
		}
		mod = call.NewModule(modID, frame.Module.Name, frame.Module.Ref, frame.Module.Pin)
	}

	args, ok := c.callArgsFromFrame(ctx, frame.Args, visiting)
	if !ok {
		return nil, false
	}
	implicitInputs, ok := c.callArgsFromFrame(ctx, frame.ImplicitInputs, visiting)
	if !ok {
		return nil, false
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
		return nil, false
	}
	return rebuilt, true
}

func (c *cache) resolveFrameRefCallID(
	ctx context.Context,
	ref *ResultCallFrameRef,
	visiting map[sharedResultID]struct{},
) (*call.ID, bool) {
	if ref == nil || ref.ResultID == 0 {
		return nil, false
	}
	refID := sharedResultID(ref.ResultID)
	frame := c.resultCallFrameSnapshot(refID)
	if frame == nil {
		return nil, false
	}
	if _, seen := visiting[refID]; seen {
		return nil, false
	}
	visiting[refID] = struct{}{}
	defer delete(visiting, refID)
	rebuilt, ok := c.callIDFromFrame(ctx, frame, visiting)
	if !ok || rebuilt == nil {
		return nil, false
	}
	for _, extra := range c.resultCallFrameExtraDigestsSnapshot(refID) {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt, true
}

func (c *cache) callArgsFromFrame(
	ctx context.Context,
	frameArgs []*ResultCallFrameArg,
	visiting map[sharedResultID]struct{},
) ([]*call.Argument, bool) {
	if len(frameArgs) == 0 {
		return nil, true
	}
	args := make([]*call.Argument, 0, len(frameArgs))
	for _, frameArg := range frameArgs {
		if frameArg == nil || frameArg.Value == nil {
			continue
		}
		lit, ok := c.callLiteralFromFrame(ctx, frameArg.Value, visiting)
		if !ok {
			return nil, false
		}
		args = append(args, call.NewArgument(frameArg.Name, lit, frameArg.IsSensitive))
	}
	return args, true
}

func (c *cache) callLiteralFromFrame(
	ctx context.Context,
	frameLit *ResultCallFrameLiteral,
	visiting map[sharedResultID]struct{},
) (call.Literal, bool) {
	if frameLit == nil {
		return nil, false
	}
	switch frameLit.Kind {
	case ResultCallFrameLiteralKindNull:
		return call.NewLiteralNull(), true
	case ResultCallFrameLiteralKindBool:
		return call.NewLiteralBool(frameLit.BoolValue), true
	case ResultCallFrameLiteralKindInt:
		return call.NewLiteralInt(frameLit.IntValue), true
	case ResultCallFrameLiteralKindFloat:
		return call.NewLiteralFloat(frameLit.FloatValue), true
	case ResultCallFrameLiteralKindString:
		return call.NewLiteralString(frameLit.StringValue), true
	case ResultCallFrameLiteralKindEnum:
		return call.NewLiteralEnum(frameLit.EnumValue), true
	case ResultCallFrameLiteralKindDigestedString:
		return call.NewLiteralDigestedString(frameLit.DigestedStringValue, frameLit.DigestedStringDigest), true
	case ResultCallFrameLiteralKindResultRef:
		id, ok := c.resolveFrameRefCallID(ctx, frameLit.ResultRef, visiting)
		if !ok || id == nil {
			return nil, false
		}
		return call.NewLiteralID(id), true
	case ResultCallFrameLiteralKindList:
		items := make([]call.Literal, 0, len(frameLit.ListItems))
		for _, item := range frameLit.ListItems {
			lit, ok := c.callLiteralFromFrame(ctx, item, visiting)
			if !ok {
				return nil, false
			}
			items = append(items, lit)
		}
		return call.NewLiteralList(items...), true
	case ResultCallFrameLiteralKindObject:
		fields := make([]*call.Argument, 0, len(frameLit.ObjectFields))
		for _, field := range frameLit.ObjectFields {
			if field == nil || field.Value == nil {
				continue
			}
			lit, ok := c.callLiteralFromFrame(ctx, field.Value, visiting)
			if !ok {
				return nil, false
			}
			fields = append(fields, call.NewArgument(field.Name, lit, field.IsSensitive))
		}
		return call.NewLiteralObject(fields...), true
	default:
		return nil, false
	}
}
