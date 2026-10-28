package dagql

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/opencontainers/go-digest"
)

// callIdentity is the identity a cache gives a call that returned an
// existing entry: the call's recipe digest, and its structural term's self
// digest and input digests, as indexWaitResultInEgraphLocked derives them
// from the call's frame when it completes.
type callIdentity struct {
	field  string
	recipe digest.Digest
	self   digest.Digest
	inputs []digest.Digest
	extras []call.ExtraDigest
}

// callIdentityFromPB derives a call's identity from the call as its span
// encodes it (ResultCall.callPB). The encoding names the receiver, the module
// and object arguments by recipe digest, so the frame decoded from it refers
// to each by a number standing for that digest, and the frame's own
// derivations then hash exactly what they hash for the call itself. The
// derived recipe digest must be the one the encoding names.
func callIdentityFromPB(pb *callpbv1.Call) (callIdentity, error) {
	if pb == nil {
		return callIdentity{}, fmt.Errorf("call identity: nil call")
	}
	dec := &callPBDecoder{}
	frame, err := dec.frame(pb)
	if err != nil {
		return callIdentity{}, fmt.Errorf("call identity %q: %w", pb.Field, err)
	}
	refDigest := func(ref *ResultCallRef) (digest.Digest, error) {
		return dec.digestOf(ref)
	}
	recipe, err := frame.digestWithInputs(refDigest)
	if err != nil {
		return callIdentity{}, fmt.Errorf("call identity %q: recipe: %w", pb.Field, err)
	}
	if recipe.String() != pb.Digest {
		return callIdentity{}, fmt.Errorf("call identity %q: derived recipe %s, encoded %s", pb.Field, recipe, pb.Digest)
	}
	self, refs, err := frame.structuralSelfDigestAndInputRefs(nil, true)
	if err != nil {
		return callIdentity{}, fmt.Errorf("call identity %q: %w", pb.Field, err)
	}
	id := callIdentity{field: pb.Field, recipe: recipe, self: self, extras: frame.ExtraDigests}
	for _, ref := range refs {
		var dig digest.Digest
		switch {
		case ref.Digest != "":
			dig = ref.Digest
		default:
			if dig, err = dec.digestOf(ref.Result); err != nil {
				return callIdentity{}, fmt.Errorf("call identity %q: input: %w", pb.Field, err)
			}
		}
		id.inputs = append(id.inputs, dig)
	}
	return id, nil
}

// callPBDecoder decodes an encoded call into a frame whose result references
// are numbers into digests.
type callPBDecoder struct {
	digests []digest.Digest
}

func (dec *callPBDecoder) ref(dig string) (*ResultCallRef, error) {
	if dig == "" {
		return nil, fmt.Errorf("empty reference digest")
	}
	dec.digests = append(dec.digests, digest.Digest(dig))
	return &ResultCallRef{ResultID: uint64(len(dec.digests))}, nil
}

func (dec *callPBDecoder) digestOf(ref *ResultCallRef) (digest.Digest, error) {
	if ref == nil || ref.ResultID == 0 || ref.ResultID > uint64(len(dec.digests)) {
		return "", fmt.Errorf("reference outside the call")
	}
	return dec.digests[ref.ResultID-1], nil
}

func (dec *callPBDecoder) frame(pb *callpbv1.Call) (*ResultCall, error) {
	if pb.Type == nil {
		return nil, fmt.Errorf("missing type")
	}
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  resultCallTypeFromPB(pb.Type),
		Field: pb.Field,
		Nth:   pb.Nth,
		View:  call.View(pb.View),
	}
	for _, extra := range pb.ExtraDigests {
		if extra == nil || extra.Digest == "" {
			continue
		}
		frame.ExtraDigests = append(frame.ExtraDigests, call.ExtraDigest{Digest: digest.Digest(extra.Digest), Label: extra.Label})
	}
	if pb.ReceiverDigest != "" {
		ref, err := dec.ref(pb.ReceiverDigest)
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
		frame.Receiver = ref
	}
	if pb.Module != nil && pb.Module.CallDigest != "" {
		ref, err := dec.ref(pb.Module.CallDigest)
		if err != nil {
			return nil, fmt.Errorf("module: %w", err)
		}
		frame.Module = &ResultCallModule{ResultRef: ref, Name: pb.Module.Name, Ref: pb.Module.Ref, Pin: pb.Module.Pin}
	}
	var err error
	if frame.Args, err = dec.args(pb.Args); err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	if frame.ImplicitInputs, err = dec.args(pb.ImplicitInputs); err != nil {
		return nil, fmt.Errorf("implicit inputs: %w", err)
	}
	return frame, nil
}

func (dec *callPBDecoder) args(pbArgs []*callpbv1.Argument) ([]*ResultCallArg, error) {
	var args []*ResultCallArg
	for _, pbArg := range pbArgs {
		if pbArg == nil {
			continue
		}
		lit, err := dec.literal(pbArg.Value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", pbArg.Name, err)
		}
		args = append(args, &ResultCallArg{Name: pbArg.Name, Value: lit})
	}
	return args, nil
}

func (dec *callPBDecoder) literal(pb *callpbv1.Literal) (*ResultCallLiteral, error) {
	if pb == nil {
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	}
	switch v := pb.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		ref, err := dec.ref(v.CallDigest)
		if err != nil {
			return nil, err
		}
		return &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: ref}, nil
	case *callpbv1.Literal_Null:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *callpbv1.Literal_Bool:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindBool, BoolValue: v.Bool}, nil
	case *callpbv1.Literal_Enum:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindEnum, EnumValue: v.Enum}, nil
	case *callpbv1.Literal_Int:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: v.Int}, nil
	case *callpbv1.Literal_Float:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindFloat, FloatValue: v.Float}, nil
	case *callpbv1.Literal_String_:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: v.String_}, nil
	case *callpbv1.Literal_Bytes:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindBytes, BytesValue: v.Bytes}, nil
	case *callpbv1.Literal_DigestedString:
		if v.DigestedString == nil {
			return nil, fmt.Errorf("empty digested string")
		}
		return &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  v.DigestedString.Value,
			DigestedStringDigest: digest.Digest(v.DigestedString.Digest),
		}, nil
	case *callpbv1.Literal_List:
		lit := &ResultCallLiteral{Kind: ResultCallLiteralKindList}
		if v.List != nil {
			for _, item := range v.List.Values {
				elem, err := dec.literal(item)
				if err != nil {
					return nil, err
				}
				lit.ListItems = append(lit.ListItems, elem)
			}
		}
		return lit, nil
	case *callpbv1.Literal_Object:
		lit := &ResultCallLiteral{Kind: ResultCallLiteralKindObject}
		if v.Object != nil {
			fields, err := dec.args(v.Object.Values)
			if err != nil {
				return nil, err
			}
			lit.ObjectFields = fields
		}
		return lit, nil
	default:
		return nil, fmt.Errorf("unknown literal %T", pb.Value)
	}
}

func resultCallTypeFromPB(pb *callpbv1.Type) *ResultCallType {
	if pb == nil {
		return nil
	}
	return &ResultCallType{NamedType: pb.NamedType, NonNull: pb.NonNull, Elem: resultCallTypeFromPB(pb.Elem)}
}
