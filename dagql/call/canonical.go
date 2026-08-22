package call

import (
	"fmt"

	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/util/hashutil"
)

// CanonicalDigest returns the recipe digest for a protobuf Call.
//
// Only fields that participate in recipe identity are hashed. In particular,
// the embedded digest and annotation fields are ignored.
func CanonicalDigest(call *callpbv1.Call) (digest.Digest, error) {
	if call == nil {
		return "", fmt.Errorf("nil call")
	}

	h := hashutil.NewHasher()
	defer func() {
		if h != nil {
			h.Close()
		}
	}()

	// ReceiverDigest (recipe identity only)
	if call.ReceiverDigest != "" {
		h.WithString(call.ReceiverDigest)
	}
	h.WithDelim()

	// Type
	for typ := call.Type; typ != nil; typ = typ.Elem {
		h.WithString(typ.NamedType)
		if typ.NonNull {
			h.WithByte(2)
		} else {
			h.WithByte(1)
		}
		h.WithDelim()
	}
	h.WithDelim()

	// Field
	h.WithString(call.Field).WithDelim()

	// Args
	for i, arg := range call.Args {
		if err := appendArgumentBytes(arg, h); err != nil {
			return "", fmt.Errorf("argument %d: %w", i, err)
		}
		h.WithDelim()
	}
	h.WithDelim()

	// Implicit inputs
	for i, input := range call.ImplicitInputs {
		if err := appendArgumentBytes(input, h); err != nil {
			return "", fmt.Errorf("implicit input %d: %w", i, err)
		}
		h.WithDelim()
	}
	h.WithDelim()

	// Module recipe digest. Module metadata is not recipe identity.
	if call.Module != nil {
		if call.Module.CallDigest == "" {
			return "", fmt.Errorf("module call digest is empty")
		}
		h.WithString(call.Module.CallDigest)
	}
	h.WithDelim()

	// Nth
	h.WithInt64(call.Nth).WithDelim()

	// View
	h.WithString(call.View).WithDelim()

	dgst := digest.Digest(h.DigestAndClose())
	h = nil
	return dgst, nil
}

// DecodeCallPayload decodes one protobuf Call payload and computes its
// canonical recipe digest. The wire payload must not claim its own digest.
func DecodeCallPayload(payload []byte) (*callpbv1.Call, digest.Digest, error) {
	call := new(callpbv1.Call)
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, call); err != nil {
		return nil, "", fmt.Errorf("unmarshal call payload: %w", err)
	}
	if call.Digest != "" {
		return nil, "", fmt.Errorf("call payload contains embedded digest %q", call.Digest)
	}

	dgst, err := CanonicalDigest(call)
	if err != nil {
		return nil, "", fmt.Errorf("compute canonical call digest: %w", err)
	}
	call.Digest = dgst.String()
	return call, dgst, nil
}

func appendArgumentBytes(arg *callpbv1.Argument, h *hashutil.Hasher) error {
	if h == nil {
		return fmt.Errorf("nil hasher")
	}
	if arg == nil {
		return fmt.Errorf("nil argument")
	}
	if arg.Value == nil {
		return fmt.Errorf("argument %q has nil literal", arg.Name)
	}

	h.WithString(arg.Name)
	if err := appendLiteralBytes(arg.Value, h); err != nil {
		return fmt.Errorf("hash argument %q: %w", arg.Name, err)
	}
	return nil
}

func appendLiteralBytes(lit *callpbv1.Literal, h *hashutil.Hasher) error {
	if h == nil {
		return fmt.Errorf("nil hasher")
	}
	if lit == nil {
		return fmt.Errorf("nil literal")
	}

	// Each kind has a unique prefix to avoid cross-kind collisions. These byte
	// tags and delimiters are the historical call.ID digest representation.
	switch value := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		if value == nil {
			return fmt.Errorf("nil call digest literal")
		}
		if value.CallDigest == "" {
			return fmt.Errorf("empty call digest literal")
		}
		h.WithByte('0').WithString(value.CallDigest)
	case *callpbv1.Literal_Null:
		if value == nil {
			return fmt.Errorf("nil null literal")
		}
		h.WithByte('1')
		if value.Null {
			h.WithByte(1)
		} else {
			h.WithByte(2)
		}
	case *callpbv1.Literal_Bool:
		if value == nil {
			return fmt.Errorf("nil bool literal")
		}
		h.WithByte('2')
		if value.Bool {
			h.WithByte(1)
		} else {
			h.WithByte(2)
		}
	case *callpbv1.Literal_Enum:
		if value == nil {
			return fmt.Errorf("nil enum literal")
		}
		h.WithByte('3').WithString(value.Enum)
	case *callpbv1.Literal_Int:
		if value == nil {
			return fmt.Errorf("nil int literal")
		}
		h.WithByte('4').WithInt64(value.Int)
	case *callpbv1.Literal_Float:
		if value == nil {
			return fmt.Errorf("nil float literal")
		}
		h.WithByte('5').WithFloat64(value.Float)
	case *callpbv1.Literal_String_:
		if value == nil {
			return fmt.Errorf("nil string literal")
		}
		h.WithByte('6').WithString(value.String_)
	case *callpbv1.Literal_List:
		if value == nil || value.List == nil {
			return fmt.Errorf("nil list literal")
		}
		h.WithByte('7')
		for i, elem := range value.List.Values {
			if err := appendLiteralBytes(elem, h); err != nil {
				return fmt.Errorf("list element %d: %w", i, err)
			}
		}
	case *callpbv1.Literal_Object:
		if value == nil || value.Object == nil {
			return fmt.Errorf("nil object literal")
		}
		h.WithByte('8')
		for i, field := range value.Object.Values {
			if err := appendArgumentBytes(field, h); err != nil {
				return fmt.Errorf("object field %d: %w", i, err)
			}
			h.WithDelim()
		}
	case *callpbv1.Literal_DigestedString:
		if value == nil || value.DigestedString == nil {
			return fmt.Errorf("nil digested string literal")
		}
		h.WithByte('9')
		if value.DigestedString.Digest != "" {
			h.WithString(value.DigestedString.Digest)
		}
	case *callpbv1.Literal_Bytes:
		if value == nil {
			return fmt.Errorf("nil bytes literal")
		}
		h.WithByte('A').WithBytes(value.Bytes...)
	case nil:
		return fmt.Errorf("literal has no value")
	default:
		return fmt.Errorf("unknown literal value type %T", value)
	}
	h.WithDelim()
	return nil
}
