package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

// DagOpDigest returns the digest used by dag-op-backed identity/effect
// propagation. It combines call self-shape bytes with equivalent input digests.
func (id *ID) DagOpDigest() digest.Digest {
	if id == nil {
		return ""
	}
	selfDigest, inputDigests, err := id.DagOpSelfDigestAndInputs()
	if err != nil {
		return id.Digest()
	}
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputDigests {
		h = h.WithString(in.String())
	}
	return digest.Digest(h.DigestAndClose())
}

func (id *ID) moduleDagOpInputDigest() digest.Digest {
	if id == nil || id.pb == nil || id.pb.Module == nil {
		return ""
	}
	if modID := id.moduleIdentityID(); modID != nil {
		return modID.OutputEquivalentDigest()
	}
	return digest.Digest(id.pb.Module.CallDigest)
}

// DagOpSelfDigestAndInputs is like SelfDigestAndInputs, but resolves ID inputs
// through output-equivalence identity for dag-op digesting.
func (id *ID) DagOpSelfDigestAndInputs() (digest.Digest, []digest.Digest, error) {
	if id == nil {
		return "", nil, nil
	}

	var inputs []digest.Digest

	h := hashutil.NewHasher()

	// Receiver contributes to inputs, not the self digest.
	if id.receiver != nil {
		inputs = append(inputs, id.receiver.OutputEquivalentDigest())
	}
	for _, dig := range normalizedExtraDigestStrings(id.pb.ExtraDigests) {
		inputs = append(inputs, digest.Digest(dig))
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
		if arg.isSensitive {
			continue
		}
		var err error
		h, inputs, err = appendArgumentDagOpSelfBytes(arg, h, inputs)
		if err != nil {
			h.Close()
			return "", nil, err
		}
		h = h.WithDelim()
	}
	h = h.WithDelim()

	// Implicit inputs
	for _, input := range id.implicitInputs {
		if input.isSensitive {
			continue
		}
		var err error
		h, inputs, err = appendArgumentDagOpSelfBytes(input, h, inputs)
		if err != nil {
			h.Close()
			return "", nil, err
		}
		h = h.WithDelim()
	}

	// Synthetic module identity input (input lane only; no self-shape bytes).
	if moduleDigest := id.moduleDagOpInputDigest(); moduleDigest != "" {
		inputs = append(inputs, moduleDigest)
	}
	// End implicit input section.
	h = h.WithDelim()

	// Nth
	h = h.WithInt64(id.pb.Nth).
		WithDelim()

	// View
	h = h.WithString(id.pb.View).
		WithDelim()

	return digest.Digest(h.DigestAndClose()), inputs, nil
}

func appendArgumentDagOpSelfBytes(arg *Argument, h *hashutil.Hasher, inputs []digest.Digest) (*hashutil.Hasher, []digest.Digest, error) {
	h = h.WithString(arg.pb.Name)

	h, inputs, err := appendLiteralDagOpSelfBytes(arg.value, h, inputs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write argument %q to hash: %w", arg.pb.Name, err)
	}

	return h, inputs, nil
}

// appendLiteralDagOpSelfBytes appends literal bytes while collecting ID literal
// output-equivalence digests as explicit inputs instead of including them in
// the hash.
func appendLiteralDagOpSelfBytes(lit Literal, h *hashutil.Hasher, inputs []digest.Digest) (*hashutil.Hasher, []digest.Digest, error) {
	var err error
	// we use a unique prefix byte for each type to avoid collisions
	switch v := lit.(type) {
	case *LiteralID:
		const prefix = '0'
		h = h.WithByte(prefix)
		inputs = append(inputs, v.id.OutputEquivalentDigest())
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
			h, inputs, err = appendLiteralDagOpSelfBytes(elem, h, inputs)
			if err != nil {
				return nil, nil, err
			}
		}
	case *LiteralObject:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, arg := range v.values {
			h, inputs, err = appendArgumentDagOpSelfBytes(arg, h, inputs)
			if err != nil {
				return nil, nil, err
			}
			h = h.WithDelim()
		}
	default:
		return nil, nil, fmt.Errorf("unknown literal type %T", v)
	}
	h = h.WithDelim()
	return h, inputs, nil
}
