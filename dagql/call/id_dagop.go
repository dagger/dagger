package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

// OutputEquivalentDigest returns the digest used when outputs can be treated as
// interchangeable across different recipes:
// 1. content digest (if set)
// 2. dag-op digest fallback
func (id *ID) OutputEquivalentDigest() digest.Digest {
	if id == nil {
		return ""
	}
	if content := id.ContentDigest(); content != "" {
		return content
	}
	return id.DagOpDigest()
}

// DagOpDigest returns the digest used by dag-op-backed identity/effect
// propagation.
//
// It is hashed in one pass like recipe digesting, except all ID inputs
// (receiver, literal IDs, module identity) use output-equivalent digests
// (content digest when available, otherwise dag-op digest).
func (id *ID) DagOpDigest() digest.Digest {
	if id == nil {
		return ""
	}
	d, err := id.calcDagOpDigest()
	if err != nil {
		return id.Digest()
	}
	return d
}

func (id *ID) calcDagOpDigest() (digest.Digest, error) {
	if id == nil {
		return "", nil
	}

	h := hashutil.NewHasher()

	// Receiver contributes output-equivalent identity.
	if id.receiver != nil {
		h = h.WithString(id.receiver.OutputEquivalentDigest().String())
	}
	h = h.WithDelim()

	// Type
	var curType *callpbv1.Type
	for curType = id.pb.Type; curType != nil; curType = curType.Elem {
		h = h.WithString(curType.NamedType)
		if curType.NonNull {
			h = h.WithByte(2)
		} else {
			h = h.WithByte(1)
		}
		h = h.WithDelim()
	}
	h = h.WithDelim()

	// Field
	h = h.WithString(id.pb.Field).
		WithDelim()

	// Args
	for _, arg := range id.args {
		arg = redactedArgForID(arg)
		if arg == nil {
			continue
		}
		var err error
		h, err = appendArgumentDagOpBytes(arg, h)
		if err != nil {
			h.Close()
			return "", err
		}
		h = h.WithDelim()
	}
	h = h.WithDelim()

	// Implicit inputs
	for _, input := range id.implicitInputs {
		input = redactedArgForID(input)
		if input == nil {
			continue
		}
		var err error
		h, err = appendArgumentDagOpBytes(input, h)
		if err != nil {
			h.Close()
			return "", err
		}
		h = h.WithDelim()
	}

	// Synthetic module identity input.
	if id.pb.Module != nil {
		moduleDigest := digest.Digest(id.pb.Module.CallDigest)
		if id.module != nil {
			if modID := id.module.ID(); modID != nil {
				moduleDigest = modID.OutputEquivalentDigest()
			}
		}
		if moduleDigest != "" {
			h = h.WithString(moduleDigest.String())
		}
	}
	// End implicit input section.
	h = h.WithDelim()

	// Nth
	h = h.WithInt64(id.pb.Nth).
		WithDelim()

	// View
	h = h.WithString(id.pb.View).
		WithDelim()

	return digest.Digest(h.DigestAndClose()), nil
}

func appendArgumentDagOpBytes(arg *Argument, h *hashutil.Hasher) (*hashutil.Hasher, error) {
	h = h.WithString(arg.pb.Name)

	h, err := appendLiteralDagOpBytes(arg.value, h)
	if err != nil {
		return nil, fmt.Errorf("failed to write argument %q to hash: %w", arg.pb.Name, err)
	}

	return h, nil
}

func appendLiteralDagOpBytes(lit Literal, h *hashutil.Hasher) (*hashutil.Hasher, error) {
	var err error
	// we use a unique prefix byte for each type to avoid collisions
	switch v := lit.(type) {
	case *LiteralID:
		const prefix = '0'
		h = h.WithByte(prefix).
			WithString(v.id.OutputEquivalentDigest().String())
	case *LiteralNull:
		const prefix = '1'
		h = h.WithByte(prefix)
		if v.pbVal.Null {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case *LiteralBool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if v.pbVal.Bool {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case *LiteralEnum:
		const prefix = '3'
		h = h.WithByte(prefix).
			WithString(v.pbVal.Enum)
	case *LiteralInt:
		const prefix = '4'
		h = h.WithByte(prefix).
			WithInt64(v.pbVal.Int)
	case *LiteralFloat:
		const prefix = '5'
		h = h.WithByte(prefix).
			WithFloat64(v.pbVal.Float)
	case *LiteralString:
		const prefix = '6'
		h = h.WithByte(prefix).
			WithString(v.pbVal.String_)
	case *LiteralList:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range v.values {
			h, err = appendLiteralDagOpBytes(elem, h)
			if err != nil {
				return nil, err
			}
		}
	case *LiteralObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, arg := range v.values {
			h, err = appendArgumentDagOpBytes(arg, h)
			if err != nil {
				return nil, err
			}
			h = h.WithDelim()
		}
	case *LiteralDigestedString:
		const prefix = '9'
		h = h.WithByte(prefix)
		if v.digest != "" {
			h = h.WithString(v.digest.String())
		}
	default:
		return nil, fmt.Errorf("unknown literal type %T", v)
	}
	h = h.WithDelim()
	return h, nil
}
