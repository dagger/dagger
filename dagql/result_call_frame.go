package dagql

import (
	"context"
	"fmt"
	"slices"
	"sort"

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
	Kind           ResultCallFrameKind    `json:"kind"`
	Type           *ResultCallFrameType   `json:"type,omitempty"`
	Field          string                 `json:"field,omitempty"`
	SyntheticOp    string                 `json:"syntheticOp,omitempty"`
	View           call.View              `json:"view,omitempty"`
	Nth            int64                  `json:"nth,omitempty"`
	EffectIDs      []string               `json:"effectIDs,omitempty"`
	Receiver       *ResultCallFrameRef    `json:"receiver,omitempty"`
	Module         *ResultCallFrameModule `json:"module,omitempty"`
	Args           []*ResultCallFrameArg  `json:"args,omitempty"`
	ImplicitInputs []*ResultCallFrameArg  `json:"implicitInputs,omitempty"`
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
		Kind:      ResultCallFrameKindField,
		Type:      NewResultCallFrameType(id.Type().ToAST()),
		Field:     id.Field(),
		View:      id.View(),
		Nth:       id.Nth(),
		EffectIDs: slices.Clone(id.EffectIDs()),
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

type callerIDFrontier map[sharedResultID]*call.ID

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

func (c *cache) persistedCallIDByResultID(ctx context.Context, resultID sharedResultID) (*call.ID, error) {
	if resultID == 0 {
		return nil, fmt.Errorf("resolve persisted call ID: zero result ID")
	}
	frame := c.resultCallFrameSnapshot(resultID)
	if frame == nil {
		return nil, fmt.Errorf("resolve persisted call ID for result %d: missing result call frame", resultID)
	}
	rebuilt, ok := c.idForCallerFromFrame(ctx, resultID, frame, callerIDFrontier{}, map[sharedResultID]struct{}{})
	if !ok || rebuilt == nil {
		return nil, fmt.Errorf("resolve persisted call ID for result %d: failed to rebuild from frame", resultID)
	}
	for _, extra := range c.resultCallFrameExtraDigestsSnapshot(resultID) {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt, nil
}

// given a rawID (which is in practice the presentation ID of a result) and a material shareResult,
// reconstruct a call.ID from the materialized result but with bias towards what the caller knows
// and was presented. E.g. the caller may know about a host directory load it made, but then that
// got deduped in cache with some earlier directory load from another client that happened to have
// same contents. In that case, the result may contain that old reference, but we still want to present
// the one the caller knows about.
// NOTE: this is just an initial best-effort implementation. A more complete one would be aware of
// results the client has loaded outside even the rawID and would do so by finding the right one for
// that client/session in the cache (which would require more client/session awareness in the cache)
func (c *cache) idForCaller(ctx context.Context, resultID sharedResultID, rawID *call.ID) *call.ID {
	if resultID == 0 || rawID == nil {
		return rawID
	}
	frame := c.resultCallFrameSnapshot(resultID)
	if frame == nil {
		return rawID
	}
	extraDigests := c.resultCallFrameExtraDigestsSnapshot(resultID)

	frontier := callerIDFrontier{}
	seedCallerIDFrontier(frame, rawID, frontier)

	rebuilt, ok := c.idForCallerFromFrame(ctx, resultID, frame, frontier, map[sharedResultID]struct{}{})
	if !ok || rebuilt == nil {
		return rawID
	}
	for _, extra := range extraDigests {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt
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

func seedCallerIDFrontier(frame *ResultCallFrame, rawID *call.ID, frontier callerIDFrontier) {
	if frame == nil || rawID == nil {
		return
	}
	if frame.Receiver != nil && rawID.Receiver() != nil {
		frontier[sharedResultID(frame.Receiver.ResultID)] = rawID.Receiver()
	}
	if frame.Module != nil && frame.Module.ResultRef != nil && rawID.Module() != nil && rawID.Module().ID() != nil {
		frontier[sharedResultID(frame.Module.ResultRef.ResultID)] = rawID.Module().ID()
	}
	seedCallerIDFrontierArgs(frame.Args, rawID.Args(), frontier)
	seedCallerIDFrontierArgs(frame.ImplicitInputs, rawID.ImplicitInputs(), frontier)
}

func seedCallerIDFrontierArgs(frameArgs []*ResultCallFrameArg, rawArgs []*call.Argument, frontier callerIDFrontier) {
	if len(frameArgs) == 0 || len(rawArgs) == 0 {
		return
	}
	for _, frameArg := range frameArgs {
		if frameArg == nil || frameArg.Value == nil {
			continue
		}
		for _, rawArg := range rawArgs {
			if rawArg == nil || rawArg.Name() != frameArg.Name {
				continue
			}
			seedCallerIDFrontierLiteral(frameArg.Value, rawArg.Value(), frontier)
			break
		}
	}
}

func seedCallerIDFrontierLiteral(frameLit *ResultCallFrameLiteral, rawLit call.Literal, frontier callerIDFrontier) {
	if frameLit == nil || rawLit == nil {
		return
	}
	switch frameLit.Kind {
	case ResultCallFrameLiteralKindResultRef:
		rawIDLit, ok := rawLit.(*call.LiteralID)
		if !ok || frameLit.ResultRef == nil || rawIDLit.Value() == nil {
			return
		}
		frontier[sharedResultID(frameLit.ResultRef.ResultID)] = rawIDLit.Value()
	case ResultCallFrameLiteralKindList:
		rawList, ok := rawLit.(*call.LiteralList)
		if !ok {
			return
		}
		for i, rawItem := range rawList.Values() {
			if i >= len(frameLit.ListItems) {
				break
			}
			item := frameLit.ListItems[i]
			seedCallerIDFrontierLiteral(item, rawItem, frontier)
		}
	case ResultCallFrameLiteralKindObject:
		rawObj, ok := rawLit.(*call.LiteralObject)
		if !ok {
			return
		}
		rawFieldsByName := map[string]*call.Argument{}
		for _, rawField := range rawObj.Args() {
			if rawField != nil {
				rawFieldsByName[rawField.Name()] = rawField
			}
		}
		for _, field := range frameLit.ObjectFields {
			if field == nil || field.Value == nil {
				continue
			}
			rawField := rawFieldsByName[field.Name]
			if rawField == nil {
				continue
			}
			seedCallerIDFrontierLiteral(field.Value, rawField.Value(), frontier)
		}
	}
}

func (c *cache) idForCallerFromFrame(
	ctx context.Context,
	resultID sharedResultID,
	frame *ResultCallFrame,
	frontier callerIDFrontier,
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
		id, ok := c.resolveFrameRefIDForCaller(ctx, frame.Receiver, frontier, visiting)
		if !ok {
			return nil, false
		}
		receiverID = id
	}
	if frame.Module != nil {
		if frame.Module.ResultRef == nil {
			return nil, false
		}
		modID, ok := c.resolveFrameRefIDForCaller(ctx, frame.Module.ResultRef, frontier, visiting)
		if !ok || modID == nil {
			return nil, false
		}
		mod = call.NewModule(modID, frame.Module.Name, frame.Module.Ref, frame.Module.Pin)
	}

	args, ok := c.callArgsForCallerFromFrame(ctx, frame.Args, frontier, visiting)
	if !ok {
		return nil, false
	}
	implicitInputs, ok := c.callArgsForCallerFromFrame(ctx, frame.ImplicitInputs, frontier, visiting)
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
	frontier[resultID] = rebuilt
	return rebuilt, true
}

func (c *cache) resolveFrameRefIDForCaller(
	ctx context.Context,
	ref *ResultCallFrameRef,
	frontier callerIDFrontier,
	visiting map[sharedResultID]struct{},
) (*call.ID, bool) {
	if ref == nil || ref.ResultID == 0 {
		return nil, false
	}
	refID := sharedResultID(ref.ResultID)
	if rebound := frontier[refID]; rebound != nil {
		return rebound, true
	}
	frame := c.resultCallFrameSnapshot(refID)
	if frame == nil {
		return nil, false
	}
	if _, seen := visiting[refID]; seen {
		return nil, false
	}
	visiting[refID] = struct{}{}
	defer delete(visiting, refID)
	rebuilt, ok := c.idForCallerFromFrame(ctx, refID, frame, frontier, visiting)
	if !ok || rebuilt == nil {
		return nil, false
	}
	for _, extra := range c.resultCallFrameExtraDigestsSnapshot(refID) {
		rebuilt = rebuilt.With(call.WithExtraDigest(extra))
	}
	return rebuilt, true
}

func (c *cache) callArgsForCallerFromFrame(
	ctx context.Context,
	frameArgs []*ResultCallFrameArg,
	frontier callerIDFrontier,
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
		lit, ok := c.callLiteralForCallerFromFrame(ctx, frameArg.Value, frontier, visiting)
		if !ok {
			return nil, false
		}
		args = append(args, call.NewArgument(frameArg.Name, lit, frameArg.IsSensitive))
	}
	return args, true
}

func (c *cache) callLiteralForCallerFromFrame(
	ctx context.Context,
	frameLit *ResultCallFrameLiteral,
	frontier callerIDFrontier,
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
		id, ok := c.resolveFrameRefIDForCaller(ctx, frameLit.ResultRef, frontier, visiting)
		if !ok || id == nil {
			return nil, false
		}
		return call.NewLiteralID(id), true
	case ResultCallFrameLiteralKindList:
		items := make([]call.Literal, 0, len(frameLit.ListItems))
		for _, item := range frameLit.ListItems {
			lit, ok := c.callLiteralForCallerFromFrame(ctx, item, frontier, visiting)
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
			lit, ok := c.callLiteralForCallerFromFrame(ctx, field.Value, frontier, visiting)
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
