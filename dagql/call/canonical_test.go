package call

import (
	"bytes"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

func canonicalDigestFixture() (input, receiver, module, final *ID) {
	typ := &ast.Type{NamedType: "Thing", NonNull: true}
	input = New().Append(typ, "input",
		WithArgs(NewArgument("seed", NewLiteralString("seed"), false)))
	receiver = New().Append(typ, "root",
		WithArgs(NewArgument("rootArg", NewLiteralInt(7), false)))
	module = New().Append(typ, "moduleSource", WithView(View("module-view")))
	final = receiver.Append(typ, "child",
		WithArgs(
			NewArgument("call", NewLiteralID(input), false),
			NewArgument("null", NewLiteralNull(), false),
			NewArgument("bool", NewLiteralBool(true), false),
			NewArgument("enum", NewLiteralEnum("VALUE"), false),
			NewArgument("int", NewLiteralInt(-42), false),
			NewArgument("float", NewLiteralFloat(3.25), false),
			NewArgument("string", NewLiteralString("value"), false),
			NewArgument("list", NewLiteralList(
				NewLiteralString("nested"),
				NewLiteralObject(NewArgument("field", NewLiteralBool(false), false)),
			), false),
			NewArgument("digested", NewLiteralDigestedString("runtime", digest.FromString("identity")), false),
			NewArgument("bytes", NewLiteralBytes([]byte{0, 1, 0xfe, 0xff}), false),
		),
		WithImplicitInputs(
			NewArgument("implicit", NewLiteralObject(
				NewArgument("key", NewLiteralInt(9), false),
			), false),
		),
		WithModule(NewModule(module, "metadata", "ref", "pin")),
		WithNth(3),
		WithView(View("child-view")),
	)
	return input, receiver, module, final
}

func TestCanonicalDigestMatchesIDConstruction(t *testing.T) {
	input, receiver, module, final := canonicalDigestFixture()
	for _, test := range []struct {
		name string
		id   *ID
		want digest.Digest
	}{
		{name: "literal call input", id: input, want: "xxh3:db127e1f279e52fe"},
		{name: "root call", id: receiver, want: "xxh3:3c521905461f6c37"},
		{name: "module call", id: module, want: "xxh3:c484fcab4bce3e55"},
		{name: "receiver call with every field", id: final, want: "xxh3:8040c7068360783e"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.id.Digest(); got != test.want {
				t.Fatalf("ID digest changed: got %s, want %s", got, test.want)
			}
			got, err := CanonicalDigest(test.id.Call())
			if err != nil {
				t.Fatalf("canonical digest: %v", err)
			}
			if got != test.want {
				t.Fatalf("canonical digest = %s, want %s", got, test.want)
			}
		})
	}
}

func TestCanonicalDigestIgnoresAnnotationsAndModuleMetadata(t *testing.T) {
	_, _, _, id := canonicalDigestFixture()
	want := id.Digest()
	call := proto.Clone(id.Call()).(*callpbv1.Call)
	call.Digest = "xxh3:embedded-annotation"
	call.EffectIds = []string{"effect-a", "effect-b"}
	call.ExtraDigests = []*callpbv1.ExtraDigest{
		{Digest: "sha256:content", Label: "content"},
	}
	call.Module.Name = "changed-name"
	call.Module.Ref = "changed-ref"
	call.Module.Pin = "changed-pin"

	got, err := CanonicalDigest(call)
	if err != nil {
		t.Fatalf("canonical digest: %v", err)
	}
	if got != want {
		t.Fatalf("annotation fields changed digest: got %s, want %s", got, want)
	}
}

func TestDecodeCallPayload(t *testing.T) {
	_, _, _, id := canonicalDigestFixture()
	source := proto.Clone(id.Call()).(*callpbv1.Call)
	source.Digest = ""
	payload, err := proto.Marshal(source)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	payload = protowire.AppendTag(payload, 1000, protowire.BytesType)
	payload = protowire.AppendBytes(payload, []byte("discard me"))

	decoded, dgst, err := DecodeCallPayload(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if dgst != id.Digest() {
		t.Fatalf("decoded digest = %s, want %s", dgst, id.Digest())
	}
	if decoded.Digest != dgst.String() {
		t.Fatalf("returned call digest = %q, want %q", decoded.Digest, dgst)
	}
	if source.Digest != "" {
		t.Fatalf("decode mutated source call digest to %q", source.Digest)
	}
	if unknown := decoded.ProtoReflect().GetUnknown(); len(unknown) != 0 {
		t.Fatalf("unknown fields were retained: %x", unknown)
	}
}

func TestDecodeCallPayloadRejectsEmbeddedDigest(t *testing.T) {
	_, _, _, id := canonicalDigestFixture()
	call := proto.Clone(id.Call()).(*callpbv1.Call)
	call.Digest = "xxh3:untrusted"
	payload, err := proto.Marshal(call)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	decoded, dgst, err := DecodeCallPayload(payload)
	if err == nil || !strings.Contains(err.Error(), "embedded digest") {
		t.Fatalf("error = %v, want embedded digest rejection", err)
	}
	if decoded != nil || dgst != "" {
		t.Fatalf("rejected payload returned call %v and digest %q", decoded, dgst)
	}
}

func TestDecodeCallPayloadTamperChangesAddress(t *testing.T) {
	_, _, _, id := canonicalDigestFixture()
	original := proto.Clone(id.Call()).(*callpbv1.Call)
	original.Digest = ""
	tampered := proto.Clone(original).(*callpbv1.Call)
	tampered.Args[6].Value = &callpbv1.Literal{
		Value: &callpbv1.Literal_String_{String_: "tampered"},
	}

	encode := func(call *callpbv1.Call) []byte {
		t.Helper()
		payload, err := proto.Marshal(call)
		if err != nil {
			t.Fatalf("marshal call: %v", err)
		}
		return payload
	}
	_, originalDigest, err := DecodeCallPayload(encode(original))
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	_, tamperedDigest, err := DecodeCallPayload(encode(tampered))
	if err != nil {
		t.Fatalf("decode tampered: %v", err)
	}
	if originalDigest == tamperedDigest {
		t.Fatalf("tamper did not change computed address %s", originalDigest)
	}
}

func TestCanonicalDigestMalformedCalls(t *testing.T) {
	arg := func(lit *callpbv1.Literal) *callpbv1.Argument {
		return &callpbv1.Argument{Name: "arg", Value: lit}
	}
	for _, test := range []struct {
		name string
		call *callpbv1.Call
	}{
		{name: "nil call", call: nil},
		{name: "nil argument", call: &callpbv1.Call{Args: []*callpbv1.Argument{nil}}},
		{name: "nil argument literal", call: &callpbv1.Call{Args: []*callpbv1.Argument{{Name: "arg"}}}},
		{name: "nil implicit input", call: &callpbv1.Call{ImplicitInputs: []*callpbv1.Argument{nil}}},
		{name: "literal without value", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{})}}},
		{name: "empty call digest literal", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{}})}}},
		{name: "nil list", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_List{}})}}},
		{name: "nil list element", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: []*callpbv1.Literal{nil}}}})}}},
		{name: "nil object", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_Object{}})}}},
		{name: "nil object field", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: []*callpbv1.Argument{nil}}}})}}},
		{name: "nil digested string", call: &callpbv1.Call{Args: []*callpbv1.Argument{arg(&callpbv1.Literal{Value: &callpbv1.Literal_DigestedString{}})}}},
		{name: "module without call digest", call: &callpbv1.Call{Module: &callpbv1.Module{Name: "broken"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if dgst, err := CanonicalDigest(test.call); err == nil {
				t.Fatalf("canonical digest = %s, want error", dgst)
			}
		})
	}
}

func TestDecodeCallPayloadMalformedInput(t *testing.T) {
	if call, dgst, err := DecodeCallPayload([]byte{0xff}); err == nil {
		t.Fatalf("invalid protobuf returned call %v and digest %s", call, dgst)
	}

	malformed := &callpbv1.Call{Args: []*callpbv1.Argument{nil}}
	payload, err := proto.Marshal(malformed)
	if err != nil {
		t.Fatalf("marshal malformed call: %v", err)
	}
	if bytes.Equal(payload, nil) {
		t.Fatal("malformed call unexpectedly encoded as empty payload")
	}
	if call, dgst, err := DecodeCallPayload(payload); err == nil {
		t.Fatalf("malformed call returned call %v and digest %s", call, dgst)
	}
}
