package dagui

import (
	"encoding/hex"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// A DB keeps every span and call frame of a session for the session's life,
// and most of the strings in them are copies: OTLP decoding allocates every
// attribute value afresh, and so does decoding a call frame. A call's digest
// arrives as its span's dag.digest and (for an object result) dag.output,
// as its frame's digest, as the receiver digest of the frames called on its
// result, and in the dag.inputs of the spans that use it; a frame's field,
// type and argument names repeat on every call of that field. The DB interns
// these as they arrive, keeping one copy of each value.
//
// Interning only shares equal strings: no value the DB holds changes.

// digestTable interns call digests. An xxh3 digest, as dagql writes them
// ("xxh3:" and 16 lowercase hex digits), is keyed by its 8 bytes; any other
// is keyed by itself.
type digestTable struct {
	xxh3  map[[8]byte]string
	other map[string]string
}

const xxh3DigestPrefix = "xxh3:"

func (t *digestTable) intern(dig string) string {
	if dig == "" {
		return ""
	}
	if key, ok := xxh3DigestKey(dig); ok {
		if interned, ok := t.xxh3[key]; ok {
			return interned
		}
		if t.xxh3 == nil {
			t.xxh3 = map[[8]byte]string{}
		}
		t.xxh3[key] = dig
		return dig
	}
	if interned, ok := t.other[dig]; ok {
		return interned
	}
	if t.other == nil {
		t.other = map[string]string{}
	}
	t.other[dig] = dig
	return dig
}

func xxh3DigestKey(dig string) (key [8]byte, ok bool) {
	if len(dig) != len(xxh3DigestPrefix)+16 || !strings.HasPrefix(dig, xxh3DigestPrefix) {
		return key, false
	}
	hexDigits := dig[len(xxh3DigestPrefix):]
	for i := range len(hexDigits) {
		c := hexDigits[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return key, false
		}
	}
	_, err := hex.Decode(key[:], []byte(hexDigits))
	return key, err == nil
}

// internDigest interns a call digest.
func (db *DB) internDigest(dig string) string {
	return db.digests.intern(dig)
}

// internSnapshotStrings interns the digests a span snapshot names, and the
// attribute values that come from a small vocabulary.
func (db *DB) internSnapshotStrings(snapshot *SpanSnapshot) {
	for _, s := range []*string{
		&snapshot.CallScope, &snapshot.ContentType, &snapshot.LLMRole,
		&snapshot.LLMTool, &snapshot.LLMToolServer, &snapshot.ActorEmoji,
		&snapshot.Message, &snapshot.TestSuiteName, &snapshot.LLMOriginKind,
	} {
		*s = db.internName(*s)
	}
	snapshot.CallDigest = db.internDigest(snapshot.CallDigest)
	snapshot.Output = db.internDigest(snapshot.Output)
	snapshot.ResumeOutput = db.internDigest(snapshot.ResumeOutput)
	snapshot.LLMCallDigest = db.internDigest(snapshot.LLMCallDigest)
	snapshot.AgentCallDigest = db.internDigest(snapshot.AgentCallDigest)
	for i, input := range snapshot.Inputs {
		snapshot.Inputs[i] = db.internDigest(input)
	}
	db.shareAttrPairs(snapshot.ExtraAttributes)
}

// internCall interns a decoded call frame's digests and names, in place.
func (db *DB) internCall(call *callpbv1.Call) {
	if call == nil {
		return
	}
	call.Digest = db.internDigest(call.Digest)
	call.ReceiverDigest = db.internDigest(call.ReceiverDigest)
	call.Field = db.internName(call.Field)
	call.View = db.internName(call.View)
	db.internType(call.Type)
	for _, arg := range call.Args {
		db.internArg(arg)
	}
	for _, arg := range call.ImplicitInputs {
		db.internArg(arg)
	}
	if mod := call.Module; mod != nil {
		mod.CallDigest = db.internDigest(mod.CallDigest)
		mod.Name = db.internName(mod.Name)
		mod.Ref = db.internName(mod.Ref)
		mod.Pin = db.internName(mod.Pin)
	}
}

func (db *DB) internType(typ *callpbv1.Type) {
	for ; typ != nil; typ = typ.Elem {
		typ.NamedType = db.internName(typ.NamedType)
	}
}

func (db *DB) internArg(arg *callpbv1.Argument) {
	if arg == nil {
		return
	}
	arg.Name = db.internName(arg.Name)
	db.internLiteral(arg.Value)
}

func (db *DB) internLiteral(lit *callpbv1.Literal) {
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		v.CallDigest = db.internDigest(v.CallDigest)
	case *callpbv1.Literal_Enum:
		v.Enum = db.internName(v.Enum)
	case *callpbv1.Literal_List:
		for _, elem := range v.List.GetValues() {
			db.internLiteral(elem)
		}
	case *callpbv1.Literal_Object:
		for _, field := range v.Object.GetValues() {
			db.internArg(field)
		}
	}
}
