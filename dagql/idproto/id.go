package idproto

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/zeebo/xxh3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func New() *ID {
	// we start with nil so there's always a nil parent at the bottom
	return nil
}

/*
ID represents a GraphQL value of a certain type, constructed by evaluating
its contained pipeline. In other words, it represents a
constructor-addressed value, which may be an object, an array, or a scalar
value.

It may be binary=>base64-encoded to be used as a GraphQL ID value for
objects. Alternatively it may be stored in a database and referred to via an
RFC-6920 ni://sha-256;... URI.

This type wraps the underlying proto type in order to enforce immutability
of its fields.

IDs are immutable from the consumer's perspective. Rather than mutating an ID,
methods on ID can be used to create a new ID on top of an existing (immutable)
Base ID (e.g. Append, SelectNth, etc.).
*/
type ID struct {
	raw *RawID_Fields

	// Wrappers around the various raw proto types in ID.raw
	base   *ID
	args   []*Argument
	module *Module
	typ    *Type
}

// The ID of the object that the field selection will be evaluated against.
//
// If nil, the root Query object is implied.
func (id *ID) Base() *ID {
	if id == nil {
		return nil
	}
	return id.base
}

// The GraphQL type of the value.
func (id *ID) Type() *Type {
	return id.typ
}

// GraphQL field name.
func (id *ID) Field() string {
	return id.raw.Field
}

// GraphQL field arguments, always in alphabetical order.
// NOTE: use with caution, any inplace writes to elements of the returned slice
// can corrupt the ID
func (id *ID) Args() []*Argument {
	return id.args
}

// If the field returns a list, this is the index of the element to select.
// Note that this defaults to zero, which means there is no selection of
// an element in the list. Non-zero indexes are 1-based.
func (id *ID) Nth() int64 {
	return id.raw.Nth
}

// Tainted returns true if the ID contains any tainted selectors.
// If true, this Selector is not reproducible.
func (id *ID) IsTainted() bool {
	if id == nil {
		return false
	}
	if id.raw.Tainted {
		return true
	}
	if id.base != nil {
		return id.base.IsTainted()
	}
	return false
}

// The module that provides the implementation of the field, if any.
func (id *ID) Module() *Module {
	return id.module
}

// Digest returns the digest of the encoded ID. It does NOT canonicalize the ID
// first.
func (id *ID) Digest() digest.Digest {
	if id == nil {
		return ""
	}
	return digest.Digest(id.raw.Digest)
}

func (id *ID) Inputs() ([]digest.Digest, error) {
	seen := map[digest.Digest]struct{}{}
	var inputs []digest.Digest
	for _, arg := range id.args {
		ins, err := arg.value.Inputs()
		if err != nil {
			return nil, err
		}
		for _, in := range ins {
			if _, ok := seen[in]; ok {
				continue
			}
			seen[in] = struct{}{}
			inputs = append(inputs, in)
		}
	}
	return inputs, nil
}

func (id *ID) Modules() []*Module {
	allMods := []*Module{}
	for id != nil {
		if id.module != nil {
			allMods = append(allMods, id.module)
		}
		for _, arg := range id.args {
			allMods = append(allMods, arg.value.Modules()...)
		}
		id = id.base
	}
	seen := map[digest.Digest]struct{}{}
	deduped := []*Module{}
	for _, mod := range allMods {
		dig := mod.id.Digest()
		if _, ok := seen[dig]; ok {
			continue
		}
		seen[dig] = struct{}{}
		deduped = append(deduped, mod)
	}
	return deduped
}

func (id *ID) Path() string {
	buf := new(bytes.Buffer)
	if id.base != nil {
		fmt.Fprintf(buf, "%s.", id.base.Path())
	}
	fmt.Fprint(buf, id.DisplaySelf())
	return buf.String()
}

func (id *ID) DisplaySelf() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s", id.raw.Field)
	for ai, arg := range id.args {
		if ai == 0 {
			fmt.Fprintf(buf, "(")
		} else {
			fmt.Fprintf(buf, ", ")
		}
		fmt.Fprintf(buf, "%s: %s", arg.raw.Name, arg.value.Display())
		if ai == len(id.args)-1 {
			fmt.Fprintf(buf, ")")
		}
	}
	if id.raw.Nth != 0 {
		fmt.Fprintf(buf, "#%d", id.raw.Nth)
	}
	return buf.String()
}

func (id *ID) Display() string {
	return fmt.Sprintf("%s: %s", id.Path(), id.typ.ToAST())
}

func (id *ID) SelectNth(nth int) *ID {
	return id.base.Append(
		id.raw.Type.Elem.toAST(),
		id.raw.Field,
		id.module,
		id.raw.Tainted,
		nth,
		id.args...,
	)
}

func (id *ID) Append(
	ret *ast.Type,
	field string,
	mod *Module,
	tainted bool,
	nth int,
	args ...*Argument,
) *ID {
	newID := &ID{
		raw: &RawID_Fields{
			BaseIDDigest: string(id.Digest()),
			Field:        field,
			Args:         make([]*RawArgument, len(args)),
			Tainted:      tainted,
			Nth:          int64(nth),
		},
		base:   id,
		module: mod,
		args:   args,
		typ:    NewType(ret),
	}

	newID.raw.Type = newID.typ.raw

	if mod != nil {
		newID.raw.Module = mod.raw
	}

	for i, arg := range args {
		if arg.Tainted() {
			newID.raw.Tainted = true
		}
		newID.raw.Args[i] = arg.raw
	}

	var err error
	newID.raw.Digest, err = newID.calcDigest()
	if err != nil {
		// something has to be deeply wrong if we can't
		// marshal proto and hash the bytes
		panic(err)
	}

	return newID
}

func (id *ID) UnderlyingTypeName() string {
	var typeName string
	elem := id.raw.Type.Elem
	for typeName == "" {
		if elem == nil {
			break
		}
		typeName = elem.NamedType
		elem = elem.Elem
	}
	return typeName
}

func (id *ID) Encode() (string, error) {
	rawID, err := id.ToProto()
	if err != nil {
		return "", fmt.Errorf("failed to convert ID to proto: %w", err)
	}

	// Deterministic is strictly needed so the IdsByDigest map is sorted in the serialized proto
	proto, err := proto.MarshalOptions{Deterministic: true}.Marshal(rawID)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ID proto: %w", err)
	}

	return base64.URLEncoding.EncodeToString(proto), nil
}

// NOTE: use with caution, any mutations to the returned proto can corrupt the ID
func (id *ID) ToProto() (*RawID, error) {
	rawID := &RawID{
		IdsByDigest: map[string]*RawID_Fields{},
	}
	id.gatherIDs(rawID.IdsByDigest)
	rawID.TopLevelIDDigest = id.raw.Digest
	return rawID, nil
}

func (id *ID) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	if id == nil {
		return
	}

	if _, ok := idsByDigest[id.raw.Digest]; ok {
		return
	}
	idsByDigest[id.raw.Digest] = id.raw

	id.base.gatherIDs(idsByDigest)
	id.module.gatherIDs(idsByDigest)
	for _, arg := range id.args {
		arg.gatherIDs(idsByDigest)
	}
}

func (id *ID) FromAnyPB(data *anypb.Any) error {
	var rawID RawID
	if err := data.UnmarshalTo(&rawID); err != nil {
		return err
	}
	return id.decode(rawID.TopLevelIDDigest, rawID.IdsByDigest, map[string]*ID{})
}

func (id *ID) Decode(str string) error {
	bytes, err := base64.URLEncoding.DecodeString(str)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}
	var rawID RawID
	if err := proto.Unmarshal(bytes, &rawID); err != nil {
		return fmt.Errorf("failed to unmarshal proto: %w", err)
	}

	return id.decode(rawID.TopLevelIDDigest, rawID.IdsByDigest, map[string]*ID{})
}

func (id *ID) decode(
	dgst string,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*ID,
) error {
	if id == nil {
		return fmt.Errorf("cannot decode into nil ID")
	}

	if existingID, ok := memo[dgst]; ok {
		*id = *existingID
		return nil
	}
	memo[dgst] = id

	raw, ok := idsByDigest[dgst]
	if !ok {
		return fmt.Errorf("ID digest %q not found", dgst)
	}
	if dgst != raw.Digest {
		// should never happen, just out of caution
		return fmt.Errorf("ID digest mismatch %q != %q", dgst, raw.Digest)
	}
	id.raw = raw

	if id.raw.BaseIDDigest != "" {
		id.base = new(ID)
		if err := id.base.decode(id.raw.BaseIDDigest, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode base ID: %w", err)
		}
	}
	if id.raw.Module != nil {
		id.module = new(Module)
		if err := id.module.decode(id.raw.Module, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode module: %w", err)
		}
	}
	for _, arg := range id.raw.Args {
		if arg == nil {
			continue
		}
		decodedArg := new(Argument)
		if err := decodedArg.decode(arg, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode argument: %w", err)
		}
		id.args = append(id.args, decodedArg)
	}
	if id.raw.Type != nil {
		id.typ = &Type{raw: id.raw.Type}
	}

	return nil
}

// presumes that id.raw.Digest is NOT set already, otherwise that value
// will be incorrectly included in the digest
func (id *ID) calcDigest() (string, error) {
	if id == nil {
		return "", nil
	}

	if id.raw.Digest != "" {
		return "", fmt.Errorf("ID digest already set")
	}

	pbBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(id.raw)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ID proto: %w", err)
	}
	h := xxh3.New()
	if _, err := h.Write(pbBytes); err != nil {
		return "", fmt.Errorf("failed to write ID proto to hash: %w", err)
	}

	return fmt.Sprintf("xxh3:%x", h.Sum(nil)), nil
}
