package call

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/zeebo/xxh3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

var marshalBufPool = &sync.Pool{New: func() any {
	b := make([]byte, 0, 1024)
	return &b
}}

var hasherPool = &sync.Pool{New: func() any {
	return xxh3.New()
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

	return id.Append(
		id.pb.Type.Elem.ToAST(),
		id.pb.Field,
		View(id.pb.View),
		id.module,
		nth,
		dgst,
	)
}

func (id *ID) Append(
	ret *ast.Type,
	field string,
	view View,
	mod *Module,
	nth int,
	customDigest digest.Digest,
	args ...*Argument,
) *ID {
	newID := &ID{
		pb: &callpbv1.Call{
			ReceiverDigest: string(id.Digest()),
			Field:          field,
			View:           string(view),
			Args:           make([]*callpbv1.Argument, 0, len(args)),
			Nth:            int64(nth),
		},
		receiver: id,
		module:   mod,
		args:     args,
		typ:      NewType(ret),
	}

	newID.pb.Type = newID.typ.pb

	if mod != nil {
		newID.pb.Module = mod.pb
	}

	for _, arg := range args {
		if arg.isSensitive {
			continue
		}
		newID.pb.Args = append(newID.pb.Args, arg.pb)
	}

	if customDigest != "" {
		newID.pb.Digest = string(customDigest)
		newID.pb.IsCustomDigest = true
	} else {
		var err error
		newID.pb.Digest, err = newID.calcDigest()
		if err != nil {
			// something has to be deeply wrong if we can't
			// marshal proto and hash the bytes
			panic(err)
		}
	}

	return newID
}

// WithDigest returns a new ID that's the same as before except with the
// given customDigest set as the ID's digest. If empty string, the default
// digest for the call will be used (based on digest of encoded call pb).
func (id *ID) WithDigest(customDigest digest.Digest) *ID {
	return id.receiver.Append(
		id.pb.Type.ToAST(),
		id.pb.Field,
		View(id.pb.View),
		id.module,
		int(id.pb.Nth),
		customDigest,
		id.args...,
	)
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

	newArgs := make([]*Argument, len(id.args))
	copy(newArgs, id.args)
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

	return id.receiver.Append(
		id.pb.Type.ToAST(),
		id.pb.Field,
		View(id.pb.View),
		id.module,
		int(id.pb.Nth),
		"", // reset to default digest
		newArgs...,
	)
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

	// re-use buffers to save some allocations and work for the go GC
	// don't do a defer Put to avoid the overhead of defers
	bufPtr := marshalBufPool.Get().(*[]byte)
	buf := *bufPtr
	buf = buf[:0]

	// also re-use xxh3 hashers since the struct has some large arrays (not slices), which
	// are expensive to allocate and (surprisingly) trigger a lot of cpu work expanding the
	// stack when not stored in heap
	h := hasherPool.Get().(*xxh3.Hasher)

	// ReceiverDigest
	buf = append(buf, []byte(id.pb.ReceiverDigest)...)
	buf = append(buf, 0)

	// Type
	var curType *callpbv1.Type
	for curType = id.pb.Type; curType != nil; curType = curType.Elem {
		buf = append(buf, []byte(curType.NamedType)...)
		buf = append(buf, 0)
		if curType.NonNull {
			buf = append(buf, 2, 0)
		} else {
			buf = append(buf, 1, 0)
		}
	}
	buf = append(buf, 0)

	// Field
	buf = append(buf, []byte(id.pb.Field)...)
	buf = append(buf, 0)

	// Args
	for _, arg := range id.pb.Args {
		buf, err = AppendArgumentBytes(arg, buf)
		if err != nil {
			marshalBufPool.Put(bufPtr)
			h.Reset()
			hasherPool.Put(h)
			return "", err
		}
	}
	buf = append(buf, 0)

	// Nth
	buf = binary.BigEndian.AppendUint64(buf, uint64(id.pb.Nth))
	buf = append(buf, 0)

	// Module
	if id.pb.Module != nil {
		buf = append(buf, []byte(id.pb.Module.CallDigest)...)
		buf = append(buf, 0)
		buf = append(buf, []byte(id.pb.Module.Name)...)
		buf = append(buf, 0)
		buf = append(buf, []byte(id.pb.Module.Ref)...)
		buf = append(buf, 0)
		buf = append(buf, []byte(id.pb.Module.Pin)...)
		buf = append(buf, 0)
	}
	buf = append(buf, 0)

	// View
	buf = append(buf, []byte(id.pb.View)...)
	buf = append(buf, 0)

	_, _ = h.Write(buf) // docs say it never errors even though it returns one

	// format as a hex string; do it the efficient way rather than fmt.Sprintf
	hashBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(hashBuf, h.Sum64())
	hexStr := make([]byte, 5+16) // 5 for "xxh3:" + 16 for the hex
	hexStr[0], hexStr[1], hexStr[2], hexStr[3], hexStr[4] = 'x', 'x', 'h', '3', ':'
	hex.Encode(hexStr[5:], hashBuf)

	marshalBufPool.Put(bufPtr)
	h.Reset()
	hasherPool.Put(h)
	return string(hexStr), nil
}

// AppendArgumentBytes appends a binary representation of the given argument to the given byte slice.
func AppendArgumentBytes(arg *callpbv1.Argument, buf []byte) ([]byte, error) {
	var err error

	buf = append(buf, []byte(arg.Name)...)
	buf = append(buf, 0)

	buf, err = appendLiteralBytes(arg.Value, buf)
	if err != nil {
		return nil, fmt.Errorf("failed to append argument %q: %w", arg.Name, err)
	}

	buf = append(buf, 0)
	return buf, nil
}

// appendLiteralBytes appends a binary representation of the given literal to the given byte slice.
func appendLiteralBytes(lit *callpbv1.Literal, buf []byte) ([]byte, error) {
	var err error
	// we use a unique prefix byte for each type to avoid collisions
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		const prefix = '0'
		buf = append(buf, prefix)
		buf = append(buf, []byte(v.CallDigest)...)
		buf = append(buf, 0)
	case *callpbv1.Literal_Null:
		const prefix = '1'
		buf = append(buf, prefix)
		if v.Null {
			buf = append(buf, prefix, 1, 0)
		} else {
			buf = append(buf, prefix, 2, 0)
		}
	case *callpbv1.Literal_Bool:
		const prefix = '2'
		buf = append(buf, prefix)
		if v.Bool {
			buf = append(buf, prefix, 1, 0)
		} else {
			buf = append(buf, prefix, 2, 0)
		}
	case *callpbv1.Literal_Enum:
		const prefix = '3'
		buf = append(buf, prefix)
		buf = append(buf, []byte(v.Enum)...)
		buf = append(buf, 0)
	case *callpbv1.Literal_Int:
		const prefix = '4'
		buf = append(buf, prefix)
		buf = binary.BigEndian.AppendUint64(buf, uint64(v.Int))
		buf = append(buf, 0)
	case *callpbv1.Literal_Float:
		const prefix = '5'
		buf = append(buf, prefix)
		buf = binary.BigEndian.AppendUint64(buf, math.Float64bits(v.Float))
		buf = append(buf, 0)
	case *callpbv1.Literal_String_:
		const prefix = '6'
		buf = append(buf, prefix)
		buf = append(buf, []byte(v.String_)...)
		buf = append(buf, 0)
	case *callpbv1.Literal_List:
		const prefix = '7'
		buf = append(buf, prefix)
		for _, elem := range v.List.Values {
			buf, err = appendLiteralBytes(elem, buf)
			if err != nil {
				return nil, err
			}
		}
		buf = append(buf, 0)
	case *callpbv1.Literal_Object:
		const prefix = '8'
		buf = append(buf, prefix)
		for _, arg := range v.Object.Values {
			buf, err = AppendArgumentBytes(arg, buf)
			if err != nil {
				return nil, err
			}
		}
		buf = append(buf, 0)
	default:
		return nil, fmt.Errorf("unknown literal type %T", v)
	}
	return buf, nil
}
