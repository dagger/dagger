package call

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/zeebo/xxh3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/util/hashutil"
)

var marshalBufPool = &sync.Pool{New: func() any {
	b := make([]byte, 0, 1024)
	return &b
}}

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

This type wraps the underlying proto DAG+Call types in order to enforce immutability
of its fields and give it a name more appropriate for how it's used in the
context of dagql + the engine.

IDs are immutable from the consumer's perspective. Rather than mutating an ID,
methods on ID can be used to create a new ID on top of an existing (immutable)
Receiver ID (e.g. Append, SelectNth, etc.).
*/
type ID struct {
	pb *callpbv1.Call

	// Wrappers around the various proto types in ID.pb
	receiver *ID
	args     []*Argument
	module   *Module
	typ      *Type
}

type View string

func (v View) String() string {
	return string(v)
}

// The ID of the object that the field selection will be evaluated against.
//
// If nil, the root Query object is implied.
func (id *ID) Receiver() *ID {
	if id == nil {
		return nil
	}
	return id.receiver
}

// The root Call of the ID, with its Digest set. Exposed so that Calls can be
// streamed over the wire one-by-one, rather than emitting full DAGs, which
// would involve a ton of duplication.
//
// WARRANTY VOID IF MUTATIONS ARE MADE TO THE INNER PROTOBUF. Perform a
// proto.Clone before mutating.
func (id *ID) Call() *callpbv1.Call {
	return id.pb
}

// The GraphQL type of the value.
func (id *ID) Type() *Type {
	return id.typ
}

// GraphQL field name.
func (id *ID) Field() string {
	return id.pb.Field
}

// GraphQL view.
func (id *ID) View() View {
	return View(id.pb.View)
}

// GraphQL field arguments, always in alphabetical order.
// NOTE: use with caution, any inplace writes to elements of the returned slice
// can corrupt the ID
func (id *ID) Args() []*Argument {
	return id.args
}

func (id *ID) Arg(name string) *Argument {
	for _, arg := range id.args {
		if arg.pb.Name == name {
			return arg
		}
	}
	return nil
}

// If the field returns a list, this is the index of the element to select.
// Note that this defaults to zero, which means there is no selection of
// an element in the list. Non-zero indexes are 1-based.
func (id *ID) Nth() int64 {
	return id.pb.Nth
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
	return digest.Digest(id.pb.Digest)
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
		id = id.receiver
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
	buf := new(strings.Builder)
	if id.receiver != nil {
		fmt.Fprintf(buf, "%s.", id.receiver.Path())
	}
	fmt.Fprint(buf, id.DisplaySelf())
	return buf.String()
}

func (id *ID) DisplaySelf() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "%s", id.pb.Field)
	for ai, arg := range id.args {
		if arg.isSensitive {
			continue
		}
		if ai == 0 {
			fmt.Fprintf(buf, "(")
		} else {
			fmt.Fprintf(buf, ", ")
		}
		fmt.Fprintf(buf, "%s: %s", arg.pb.Name, arg.value.Display())
		if ai == len(id.args)-1 {
			fmt.Fprintf(buf, ")")
		}
	}
	if id.pb.Nth != 0 {
		fmt.Fprintf(buf, "#%d", id.pb.Nth)
	}
	return buf.String()
}

func (id *ID) Display() string {
	if id == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", id.Path(), id.typ.ToAST())
}

func (id *ID) Name() string {
	name := id.pb.Field
	if id.receiver != nil {
		name = id.receiver.typ.NamedType() + "." + name
	}
	return name
}

// Return a new ID that's the selection of the nth element of the return value of the existing ID.
// The new digest is derived from the existing ID's digest and the nth index.
func (id *ID) SelectNth(nth int) *ID {
	buf := []byte(id.Digest())
	buf = binary.LittleEndian.AppendUint64(buf, uint64(nth))
	h := xxh3.New()
	h.Write(buf)
	dgst := digest.NewDigest("xxh3", h)
	return id.With(
		WithReceiver(id),
		WithNth(nth),
		WithType(id.pb.Type.Elem.ToAST()),
		WithCustomDigest(dgst),
	)
}

type IDOpt func(*ID)

func WithModule(mod *Module) IDOpt {
	return func(id *ID) {
		if mod != nil {
			id.module = mod
			id.pb.Module = mod.pb
		} else {
			id.module = nil
			id.pb.Module = nil
		}
	}
}

func WithType(typ *ast.Type) IDOpt {
	return func(id *ID) {
		id.typ = NewType(typ)
		id.pb.Type = id.typ.pb
	}
}

func WithNth(n int) IDOpt {
	return func(id *ID) {
		id.pb.Nth = int64(n)
	}
}

func WithView(view View) IDOpt {
	return func(id *ID) {
		id.pb.View = view.String()
	}
}

func WithCustomDigest(dig digest.Digest) IDOpt {
	return func(id *ID) {
		if dig != "" {
			id.pb.Digest = dig.String()
			id.pb.IsCustomDigest = true
		} else {
			id.pb.Digest = ""
			id.pb.IsCustomDigest = false
		}
	}
}

func WithArgs(args ...*Argument) IDOpt {
	return func(id *ID) {
		id.args = args
		id.pb.Args = make([]*callpbv1.Argument, 0, len(args))
		for _, arg := range args {
			if arg.isSensitive {
				continue
			}
			id.pb.Args = append(id.pb.Args, arg.pb)
		}
	}
}

func WithReceiver(recv *ID) IDOpt {
	return func(id *ID) {
		id.receiver = recv
		if recv != nil {
			id.pb.ReceiverDigest = recv.pb.Digest
		} else {
			id.pb.ReceiverDigest = ""
		}
	}
}

func (id *ID) With(opts ...IDOpt) *ID {
	return id.shallowClone().apply(opts...)
}

func (id *ID) Append(ret *ast.Type, field string, opts ...IDOpt) *ID {
	typ := NewType(ret)
	newID := &ID{
		pb: &callpbv1.Call{
			Type:           typ.pb,
			ReceiverDigest: id.Digest().String(),
			Field:          field,
		},
		receiver: id,
		typ:      typ,
	}
	return newID.apply(opts...)
}

// WithDigest returns a new ID that's the same as before except with the
// given customDigest set as the ID's digest. If empty string, the default
// digest for the call will be used (based on digest of encoded call pb).
func (id *ID) WithDigest(customDigest digest.Digest) *ID {
	return id.With(WithCustomDigest(customDigest))
}

func (id *ID) HasCustomDigest() bool {
	if id == nil {
		return false
	}
	return id.pb.IsCustomDigest
}

// WithArgument returns a new ID that's the same as before except with the
// given argument added to the ID's arguments. If an argument with the same
// name already exists, it will be replaced with the new one. The digest will
// reset to the default "recipe-based" value, so any custom one needs to be
// set after this call via WithDigest.
func (id *ID) WithArgument(arg *Argument) *ID {
	if id == nil {
		return nil
	}

	newArgs := slices.Clone(id.args)
	var replaced bool
	for i, existingArg := range newArgs {
		if existingArg.pb.Name == arg.pb.Name {
			// replace existing argument with the new one
			newArgs[i] = arg
			replaced = true
			break
		}
	}
	if !replaced {
		newArgs = append(newArgs, arg)
	}

	return id.With(WithArgs(newArgs...))
}

func (id *ID) Encode() (string, error) {
	if id == nil {
		return "", nil
	}
	dagPB, err := id.ToProto()
	if err != nil {
		return "", fmt.Errorf("failed to convert ID to proto: %w", err)
	}

	buf := *(marshalBufPool.Get().(*[]byte))
	buf = buf[:0]
	defer marshalBufPool.Put(&buf)
	// Deterministic is strictly needed so the CallsByDigest map is sorted in the serialized proto
	proto, err := proto.MarshalOptions{Deterministic: true}.MarshalAppend(buf, dagPB)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ID proto: %w", err)
	}

	return base64.StdEncoding.EncodeToString(proto), nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	enc, err := id.Encode()
	if err != nil {
		return nil, err
	}
	return []byte(`"` + enc + `"`), nil
}

func (id *ID) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("invalid JSON string")
	}
	enc := string(data[1 : len(data)-1])
	return id.Decode(enc)
}

// NOTE: use with caution, any mutations to the returned proto can corrupt the ID
func (id *ID) ToProto() (*callpbv1.DAG, error) {
	dagPB := &callpbv1.DAG{
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	id.gatherCalls(dagPB.CallsByDigest)
	dagPB.RootDigest = id.pb.Digest
	return dagPB, nil
}

func (id *ID) FromProto(dagPB *callpbv1.DAG) error {
	if id == nil {
		return fmt.Errorf("cannot decode into nil ID")
	}
	if err := id.decode(dagPB.RootDigest, dagPB.CallsByDigest, map[string]*ID{}); err != nil {
		return fmt.Errorf("failed to decode DAG: %w", err)
	}
	return nil
}

func (id *ID) shallowClone() *ID {
	cp := *id
	// NB: this is finnicky, but shouldn't change much, seems worth avoiding
	// reflection in proto.CloneOf
	cp.pb = &callpbv1.Call{
		ReceiverDigest: cp.pb.ReceiverDigest,
		Type:           cp.pb.Type,
		Field:          cp.pb.Field,
		Args:           cp.pb.Args, // NOTE: no slices.Clone here - ALWAYS use WithArgs
		Nth:            cp.pb.Nth,
		Module:         cp.pb.Module,
		Digest:         cp.pb.Digest,
		View:           cp.pb.View,
		IsCustomDigest: cp.pb.IsCustomDigest,
	}
	return &cp
}

func (id *ID) apply(opts ...IDOpt) *ID {
	// clear any existing digest; must be re-applied each time
	id.pb.Digest = ""
	id.pb.IsCustomDigest = false
	for _, opt := range opts {
		opt(id)
	}
	if !id.HasCustomDigest() {
		var err error
		id.pb.Digest, err = id.calcDigest()
		if err != nil {
			// something has to be deeply wrong if we can't
			// marshal proto and hash the bytes
			panic(err)
		}
	}
	return id
}

func (id *ID) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	if id == nil {
		return
	}

	if _, ok := callsByDigest[id.pb.Digest]; ok {
		return
	}
	callsByDigest[id.pb.Digest] = id.pb

	id.receiver.gatherCalls(callsByDigest)
	id.module.gatherCalls(callsByDigest)
	for _, arg := range id.args {
		arg.gatherCalls(callsByDigest)
	}
}

func (id *ID) FromAnyPB(data *anypb.Any) error {
	var dagPB callpbv1.DAG
	if err := data.UnmarshalTo(&dagPB); err != nil {
		return err
	}
	return id.decode(dagPB.RootDigest, dagPB.CallsByDigest, map[string]*ID{})
}

func (id *ID) Decode(str string) error {
	bytes, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}
	var dagPB callpbv1.DAG
	if err := proto.Unmarshal(bytes, &dagPB); err != nil {
		return fmt.Errorf("failed to unmarshal proto: %w", err)
	}

	return id.decode(dagPB.RootDigest, dagPB.CallsByDigest, map[string]*ID{})
}

func (id *ID) decode(
	dgst string,
	callsByDigest map[string]*callpbv1.Call,
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

	pb, ok := callsByDigest[dgst]
	if !ok {
		return fmt.Errorf("call digest %q not found", dgst)
	}
	if dgst != pb.Digest {
		// should never happen, just out of caution
		return fmt.Errorf("call digest mismatch %q != %q", dgst, pb.Digest)
	}
	id.pb = pb

	if id.pb.ReceiverDigest != "" {
		id.receiver = new(ID)
		if err := id.receiver.decode(id.pb.ReceiverDigest, callsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode receiver Call: %w", err)
		}
	}
	if id.pb.Module != nil {
		id.module = new(Module)
		if err := id.module.decode(id.pb.Module, callsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode module: %w", err)
		}
	}
	for _, arg := range id.pb.Args {
		if arg == nil {
			continue
		}
		decodedArg := new(Argument)
		if err := decodedArg.decode(arg, callsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode argument: %w", err)
		}
		id.args = append(id.args, decodedArg)
	}
	if id.pb.Type != nil {
		id.typ = &Type{pb: id.pb.Type}
	}

	return nil
}

// presumes that id.pb.Digest are NOT set already,
// otherwise those values will be incorrectly included in the digest
func (id *ID) calcDigest() (string, error) {
	if id == nil {
		return "", nil
	}

	if id.pb.Digest != "" {
		return "", fmt.Errorf("call digest already set")
	}

	var err error

	h := hashutil.NewHasher()

	// ReceiverDigest
	h = h.WithString(id.pb.ReceiverDigest).
		WithDelim()

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
	for _, arg := range id.pb.Args {
		h, err = AppendArgumentBytes(arg, h)
		if err != nil {
			h.Close()
			return "", err
		}
		h = h.WithDelim()
	}
	h = h.WithDelim()

	// Nth
	h = h.WithInt64(id.pb.Nth).
		WithDelim()

	// Module
	if id.pb.Module != nil {
		h = h.WithString(id.pb.Module.CallDigest).
			WithString(id.pb.Module.Name).
			WithString(id.pb.Module.Ref).
			WithString(id.pb.Module.Pin)
	}
	h = h.WithDelim()

	// View
	h = h.WithString(id.pb.View).
		WithDelim()

	return h.DigestAndClose(), nil
}

// AppendArgumentBytes appends a binary representation of the given argument to the given byte slice.
func AppendArgumentBytes(arg *callpbv1.Argument, h *hashutil.Hasher) (*hashutil.Hasher, error) {
	h = h.WithString(arg.Name)

	h, err := appendLiteralBytes(arg.Value, h)
	if err != nil {
		return nil, fmt.Errorf("failed to write argument %q to hash: %w", arg.Name, err)
	}

	return h, nil
}

// appendLiteralBytes appends a binary representation of the given literal to the given byte slice.
func appendLiteralBytes(lit *callpbv1.Literal, h *hashutil.Hasher) (*hashutil.Hasher, error) {
	var err error
	// we use a unique prefix byte for each type to avoid collisions
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		const prefix = '0'
		h = h.WithByte(prefix).
			WithString(v.CallDigest)
	case *callpbv1.Literal_Null:
		const prefix = '1'
		h = h.WithByte(prefix)
		if v.Null {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case *callpbv1.Literal_Bool:
		const prefix = '2'
		h = h.WithByte(prefix)
		if v.Bool {
			h = h.WithByte(1)
		} else {
			h = h.WithByte(2)
		}
	case *callpbv1.Literal_Enum:
		const prefix = '3'
		h = h.WithByte(prefix).
			WithString(v.Enum)
	case *callpbv1.Literal_Int:
		const prefix = '4'
		h = h.WithByte(prefix).
			WithInt64(v.Int)
	case *callpbv1.Literal_Float:
		const prefix = '5'
		h = h.WithByte(prefix).
			WithFloat64(v.Float)
	case *callpbv1.Literal_String_:
		const prefix = '6'
		h = h.WithByte(prefix).
			WithString(v.String_)
	case *callpbv1.Literal_List:
		const prefix = '7'
		h = h.WithByte(prefix)
		for _, elem := range v.List.Values {
			h, err = appendLiteralBytes(elem, h)
			if err != nil {
				return nil, err
			}
		}
	case *callpbv1.Literal_Object:
		const prefix = '8'
		h = h.WithByte(prefix)
		for _, arg := range v.Object.Values {
			h, err = AppendArgumentBytes(arg, h)
			if err != nil {
				return nil, err
			}
			h = h.WithDelim()
		}
	default:
		return nil, fmt.Errorf("unknown literal type %T", v)
	}
	h = h.WithDelim()
	return h, nil
}
