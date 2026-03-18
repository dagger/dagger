package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync"

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

	// cache is a runtime-only backpointer used for recursive digest resolution
	// through ResultCallRef.ResultID. It is not persisted.
	//
	// Cache-owned frames are treated as immutable once attached. Read-only
	// identity paths borrow them directly; mutation paths must fork first.
	cache *cache

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

	cache *cache
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
		cache:        frame.cache,
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
		cache:          frame.cache,
	}
}

func (frame *ResultCall) RecipeDigest() (digest.Digest, error) {
	return frame.recipeDigestWithVisiting(map[sharedResultID]struct{}{})
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

func (frame *ResultCall) ContentPreferredDigest() (digest.Digest, error) {
	return frame.contentPreferredDigestWithVisiting(map[sharedResultID]struct{}{})
}

func (frame *ResultCall) RecipeID() (*call.ID, error) {
	return frame.recipeIDWithVisiting(map[sharedResultID]struct{}{})
}

func (frame *ResultCall) recipeDigestWithVisiting(visiting map[sharedResultID]struct{}) (digest.Digest, error) {
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
			receiverDigest, err := recipeDigestForResultCallRef(frame.cache, frame.Receiver, visiting)
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
			nextH, err := appendResultCallArgBytes(frame.cache, arg, h, visiting)
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
			nextH, err := appendResultCallArgBytes(frame.cache, input, h, visiting)
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
			moduleDigest, err := recipeDigestForResultCallRef(frame.cache, frame.Module.ResultRef, visiting)
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

func (frame *ResultCall) contentPreferredDigestWithVisiting(visiting map[sharedResultID]struct{}) (digest.Digest, error) {
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
			receiverDigest, err := contentPreferredDigestForResultCallRef(frame.cache, frame.Receiver, visiting)
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
			nextH, err := appendResultCallArgContentPreferredBytes(frame.cache, arg, h, visiting)
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
			nextH, err := appendResultCallArgContentPreferredBytes(frame.cache, input, h, visiting)
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
			moduleDigest, err := contentPreferredDigestForResultCallRef(frame.cache, frame.Module.ResultRef, visiting)
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

func (frame *ResultCall) SelfDigestAndInputRefs() (digest.Digest, []ResultCallStructuralInputRef, error) {
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
			cache:  frame.cache,
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
		nextH, nextInputRefs, err := appendResultCallArgSelfRefs(frame.cache, arg, h, inputRefs)
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
		nextH, nextInputRefs, err := appendResultCallArgSelfRefs(frame.cache, input, h, inputRefs)
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
			if h != nil {
				h.Close()
			}
			return "", nil, fmt.Errorf("result call frame %q structural inputs: %w", field, err)
		}
	}

	return digest.Digest(h.DigestAndClose()), inputRefs, nil
}

func (frame *ResultCall) AllEffectIDs() ([]string, error) {
	if frame == nil {
		return nil, nil
	}
	seenResults := map[sharedResultID]struct{}{}
	seenEffects := map[string]struct{}{}
	var out []string

	var walkFrame func(*ResultCall) error
	var walkRef func(*ResultCallRef) error
	var walkLiteral func(*ResultCallLiteral) error

	walkRef = func(ref *ResultCallRef) error {
		if ref == nil {
			return nil
		}
		if ref.Call != nil {
			return walkFrame(ref.Call)
		}
		if ref.ResultID == 0 {
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
		target := frame.cache.resultCallByResultID(resultID)
		if target == nil {
			return fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
		}
		return walkFrame(target)
	}

	walkLiteral = func(lit *ResultCallLiteral) error {
		if lit == nil {
			return nil
		}
		switch lit.Kind {
		case ResultCallLiteralKindResultRef:
			return walkRef(lit.ResultRef)
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

	walkFrame = func(cur *ResultCall) error {
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

func (frame *ResultCall) bindCache(c *cache) {
	if frame == nil {
		return
	}
	frame.cache = c
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

func (ref ResultCallStructuralInputRef) InputDigest() (digest.Digest, error) {
	switch {
	case ref.Result != nil && ref.Digest != "":
		return "", fmt.Errorf("structural input ref cannot have both result and digest")
	case ref.Result != nil:
		return recipeDigestForResultCallRef(ref.cache, ref.Result, map[sharedResultID]struct{}{})
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
	}
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
	frame := c.resultCallByResultID(resultID)
	if frame == nil {
		return "", fmt.Errorf("missing result call frame for shared result %d", resultID)
	}

	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	return frame.recipeDigestWithVisiting(visiting)
}

func recipeDigestForResultCallRef(c *cache, ref *ResultCallRef, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	if ref.Call != nil {
		ref.Call.bindCache(c)
		return ref.Call.recipeDigestWithVisiting(visiting)
	}
	return recipeDigestForCachedResult(c, sharedResultID(ref.ResultID), visiting)
}

func contentPreferredDigestForCachedResult(c *cache, resultID sharedResultID, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if resultID == 0 {
		return "", fmt.Errorf("missing result ref")
	}
	if c == nil {
		return "", fmt.Errorf("cannot resolve result ref %d without cache", resultID)
	}
	if _, seen := visiting[resultID]; seen {
		return "", fmt.Errorf("cycle while reconstructing content-preferred digest for shared result %d", resultID)
	}
	frame := c.resultCallByResultID(resultID)
	if frame == nil {
		return "", fmt.Errorf("missing result call frame for shared result %d", resultID)
	}

	visiting[resultID] = struct{}{}
	defer delete(visiting, resultID)

	return frame.contentPreferredDigestWithVisiting(visiting)
}

func contentPreferredDigestForResultCallRef(c *cache, ref *ResultCallRef, visiting map[sharedResultID]struct{}) (digest.Digest, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	if ref.Call != nil {
		ref.Call.bindCache(c)
		return ref.Call.contentPreferredDigestWithVisiting(visiting)
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
	c *cache,
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
	c *cache,
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
	c *cache,
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
	c *cache,
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
	c *cache,
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
	c *cache,
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
			cache:  c,
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

func (c *cache) resultCallLiteralFromCallLiteralLocked(
	ctx context.Context,
	lit call.Literal,
) (*ResultCallLiteral, error) {
	switch v := lit.(type) {
	case nil:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralNull:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralBool:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindBool, BoolValue: v.Value()}, nil
	case *call.LiteralInt:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: v.Value()}, nil
	case *call.LiteralFloat:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindFloat, FloatValue: v.Value()}, nil
	case *call.LiteralString:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: v.Value()}, nil
	case *call.LiteralEnum:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindEnum, EnumValue: v.Value()}, nil
	case *call.LiteralDigestedString:
		return &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  v.Value(),
			DigestedStringDigest: v.Digest(),
		}, nil
	case *call.LiteralID:
		ref, err := c.resultCallRefForInputIDLocked(ctx, v.Value())
		if err != nil {
			return nil, fmt.Errorf("frame literal id %s: %w", v.Value().Digest(), err)
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	case *call.LiteralList:
		items := make([]*ResultCallLiteral, 0, v.Len())
		for _, item := range v.Values() {
			converted, err := c.resultCallLiteralFromCallLiteralLocked(ctx, item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindList,
			ListItems: items,
		}, nil
	case *call.LiteralObject:
		fields := make([]*ResultCallArg, 0, v.Len())
		for _, field := range v.Args() {
			converted, err := c.resultCallLiteralFromCallLiteralLocked(ctx, field.Value())
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name(), err)
			}
			fields = append(fields, &ResultCallArg{
				Name:        field.Name(),
				IsSensitive: field.IsSensitive(),
				Value:       converted,
			})
		}
		return &ResultCallLiteral{
			Kind:         ResultCallLiteralKindObject,
			ObjectFields: fields,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported literal %T", lit)
	}
}

func (c *cache) resultCallRefForInputIDLocked(ctx context.Context, inputID *call.ID) (*ResultCallRef, error) {
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
	return &ResultCallRef{ResultID: uint64(shared.id)}, nil
}

func (c *cache) RecipeIDForCall(frame *ResultCall) (*call.ID, error) {
	if frame == nil {
		return nil, fmt.Errorf("rebuild recipe ID: nil call")
	}
	frame.bindCache(c)
	return frame.RecipeID()
}

func (c *cache) RecipeDigestForCall(frame *ResultCall) (digest.Digest, error) {
	if frame == nil {
		return "", fmt.Errorf("derive recipe digest: nil call")
	}
	frame.bindCache(c)
	return frame.RecipeDigest()
}

// resultCallByResultID returns the cache-owned immutable result-call frame for
// the given shared result. Callers must treat the returned frame as read-only.
func (c *cache) resultCallByResultID(resultID sharedResultID) *ResultCall {
	if resultID == 0 {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[resultID]
	if res == nil || res.resultCall == nil {
		return nil
	}
	return res.resultCall
}

func (frame *ResultCall) recipeIDWithVisiting(visiting map[sharedResultID]struct{}) (*call.ID, error) {
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
		id, err := frame.resolveRefRecipeID(frame.Receiver, visiting)
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
		receiverID = id
	}
	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			return nil, fmt.Errorf("module: missing result ref")
		}
		modID, err := frame.resolveRefRecipeID(frame.Module.ResultRef, visiting)
		if err != nil {
			return nil, fmt.Errorf("module: %w", err)
		}
		mod = call.NewModule(modID, frame.Module.Name, frame.Module.Ref, frame.Module.Pin)
	}

	args, err := frame.callArgs(frame.Args, visiting)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	implicitInputs, err := frame.callArgs(frame.ImplicitInputs, visiting)
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

func (frame *ResultCall) resolveRefRecipeID(ref *ResultCallRef, visiting map[sharedResultID]struct{}) (*call.ID, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Call != nil {
		ref.Call.bindCache(frame.cache)
		return ref.Call.recipeIDWithVisiting(visiting)
	}
	if frame == nil || frame.cache == nil {
		return nil, fmt.Errorf("cannot resolve result ref %d without cache", ref.ResultID)
	}
	refID := sharedResultID(ref.ResultID)
	refFrame := frame.cache.resultCallByResultID(refID)
	if refFrame == nil {
		return nil, fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
	}
	if _, seen := visiting[refID]; seen {
		return nil, fmt.Errorf("cycle while reconstructing recipe ID for shared result %d", ref.ResultID)
	}
	visiting[refID] = struct{}{}
	defer delete(visiting, refID)
	return refFrame.recipeIDWithVisiting(visiting)
}

func (frame *ResultCall) callArgs(
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
		lit, err := frame.callLiteral(frameArg.Value, visiting)
		if err != nil {
			return nil, err
		}
		args = append(args, call.NewArgument(frameArg.Name, lit, frameArg.IsSensitive))
	}
	return args, nil
}

func (frame *ResultCall) callLiteral(
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
		id, err := frame.resolveRefRecipeID(frameLit.ResultRef, visiting)
		if err != nil {
			return nil, err
		}
		return call.NewLiteralID(id), nil
	case ResultCallLiteralKindList:
		items := make([]call.Literal, 0, len(frameLit.ListItems))
		for _, item := range frameLit.ListItems {
			lit, err := frame.callLiteral(item, visiting)
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
			lit, err := frame.callLiteral(field.Value, visiting)
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
