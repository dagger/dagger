package call

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	fn()
}

func TestHandleIDRoundTrip(t *testing.T) {
	typ := NewType(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	})
	orig := NewEngineResultID(42, typ)

	if !orig.IsHandle() {
		t.Fatal("expected handle-form ID")
	}
	if got := orig.EngineResultID(); got != 42 {
		t.Fatalf("unexpected engine result id: got %d, want 42", got)
	}

	enc, err := orig.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded := new(ID)
	if err := decoded.Decode(enc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !decoded.IsHandle() {
		t.Fatal("expected decoded handle-form ID")
	}
	if got := decoded.EngineResultID(); got != 42 {
		t.Fatalf("unexpected decoded engine result id: got %d, want 42", got)
	}
	if got := decoded.Type().NamedType(); got != "String" {
		t.Fatalf("unexpected decoded type: got %q, want String", got)
	}
	decodedEnc, err := decoded.Encode()
	if err != nil {
		t.Fatalf("decoded encode: %v", err)
	}
	if decodedEnc != enc {
		t.Fatalf("handle encoding mismatch after round-trip: got %q, want %q", decodedEnc, enc)
	}
}

func TestHandleIDRecipeOperationsPanic(t *testing.T) {
	id := NewEngineResultID(7, NewType(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}))

	requirePanic(t, func() { _ = id.Call() })
	requirePanic(t, func() { _ = id.Digest() })
	requirePanic(t, func() { _ = id.Field() })
	requirePanic(t, func() { _ = id.Args() })
	requirePanic(t, func() { _ = id.Append(&ast.Type{NamedType: "String"}, "child") })
}

func TestLiteralIDRejectsHandleID(t *testing.T) {
	id := NewEngineResultID(9, NewType(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}))

	requirePanic(t, func() { _ = NewLiteralID(id) })
}

func TestImplicitInputsAffectDigest(t *testing.T) {
	base := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "field")

	clientA := base.With(WithImplicitInputs(
		NewArgument("cachePerClient", NewLiteralString("client-a"), false),
	))
	clientB := base.With(WithImplicitInputs(
		NewArgument("cachePerClient", NewLiteralString("client-b"), false),
	))

	if clientA.Digest() == clientB.Digest() {
		t.Fatalf("expected different digests for different implicit inputs: %s", clientA.Digest())
	}

	// implicit inputs are intentionally excluded from human-readable display for now
	if strings.Contains(clientA.DisplaySelf(), "cachePerClient") {
		t.Fatalf("implicit input leaked into display: %s", clientA.DisplaySelf())
	}
}

func TestImplicitInputsRoundTrip(t *testing.T) {
	orig := New().Append(
		&ast.Type{
			NamedType: "String",
			NonNull:   true,
		},
		"field",
		WithArgs(NewArgument("explicit", NewLiteralString("arg"), false)),
		WithImplicitInputs(NewArgument("cachePerClient", NewLiteralString("client-a"), false)),
	)

	enc, err := orig.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded := new(ID)
	if err := decoded.Decode(enc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Digest() != orig.Digest() {
		t.Fatalf("digest mismatch after round-trip: got %s, want %s", decoded.Digest(), orig.Digest())
	}

	implicitInputs := decoded.ImplicitInputs()
	if len(implicitInputs) != 1 {
		t.Fatalf("expected 1 implicit input, got %d", len(implicitInputs))
	}
	if implicitInputs[0].Name() != "cachePerClient" {
		t.Fatalf("unexpected implicit input name: %q", implicitInputs[0].Name())
	}

	val, ok := implicitInputs[0].Value().(*LiteralString)
	if !ok {
		t.Fatalf("unexpected implicit input literal type: %T", implicitInputs[0].Value())
	}
	if val.Value() != "client-a" {
		t.Fatalf("unexpected implicit input value: %q", val.Value())
	}
}

func TestModuleMetadataDoesNotAffectIdentity(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	moduleID := New().Append(typ, "module")
	moduleA := NewModule(moduleID, "mod-a", "ref-a", "pin-a")
	moduleB := NewModule(moduleID, "mod-b", "ref-b", "pin-b")

	base := New().Append(typ, "field")
	idA := base.With(WithModule(moduleA))
	idB := base.With(WithModule(moduleB))

	if idA.Digest() != idB.Digest() {
		t.Fatalf("module metadata should not affect digest: %s vs %s", idA.Digest(), idB.Digest())
	}
}

func TestModuleIdentityAffectsDigest(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	sharedModuleContent := digest.FromString("module-content")
	moduleAID := New().Append(typ, "moduleA").With(WithContentDigest(sharedModuleContent))
	moduleBID := New().Append(typ, "moduleB").With(WithContentDigest(sharedModuleContent))

	idNoModule := New().Append(typ, "field")
	idWithModuleA := idNoModule.With(WithModule(NewModule(moduleAID, "mod", "ref", "pin")))
	idWithModuleB := idNoModule.With(WithModule(NewModule(moduleBID, "mod", "ref", "pin")))

	if idNoModule.Digest() == idWithModuleA.Digest() {
		t.Fatalf("module identity should affect recipe digest: %s vs %s", idNoModule.Digest(), idWithModuleA.Digest())
	}
	if idWithModuleA.Digest() == idWithModuleB.Digest() {
		t.Fatalf("distinct module identities should affect recipe digest: %s vs %s", idWithModuleA.Digest(), idWithModuleB.Digest())
	}
}

func TestExtraDigestsFromLegacyUnspecifiedEntries(t *testing.T) {
	contentDigest := digest.FromString("legacy-content")
	additionalDigest := digest.FromString("legacy-additional")

	id := &ID{
		pb: &callpbv1.Call{
			ExtraDigests: []*callpbv1.ExtraDigest{
				{Digest: contentDigest.String(), Label: "content"},
				{Digest: additionalDigest.String(), Label: ""},
			},
		},
	}

	extras := id.ExtraDigests()
	if len(extras) != 2 {
		t.Fatalf("expected 2 extra digests, got %d", len(extras))
	}

	byDigest := map[digest.Digest]ExtraDigest{}
	for _, extra := range extras {
		byDigest[extra.Digest] = extra
	}

	if got := byDigest[contentDigest].Label; got != "content" {
		t.Fatalf("content digest label mismatch: got %q", got)
	}
	if got := byDigest[additionalDigest].Label; got != "" {
		t.Fatalf("additional digest label mismatch: got %q", got)
	}
}

func TestExtraDigestsRoundTripThroughProto(t *testing.T) {
	auxDigest := digest.FromString("explicit-aux")
	orig := New().Append(
		&ast.Type{
			NamedType: "String",
			NonNull:   true,
		},
		"field",
		WithExtraDigest(ExtraDigest{
			Digest: auxDigest,
			Label:  "auxiliary-example",
		}),
	)

	enc, err := orig.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded := new(ID)
	if err := decoded.Decode(enc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	extras := decoded.ExtraDigests()
	if len(extras) != 1 {
		t.Fatalf("expected 1 extra digest, got %d", len(extras))
	}
	if extras[0].Digest != auxDigest {
		t.Fatalf("digest mismatch: got %s, want %s", extras[0].Digest, auxDigest)
	}
	if extras[0].Label != "auxiliary-example" {
		t.Fatalf("label mismatch: got %q", extras[0].Label)
	}
}

func TestDigestedStringUsesAttachedDigestForIdentity(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	execMDDigest := digest.FromString("execmd-identity")

	idA := New().Append(
		typ,
		"withExec",
		WithArgs(NewArgument("execMD", NewLiteralDigestedString(`{"clientID":"a","execID":"1"}`, execMDDigest), false)),
	)
	idB := New().Append(
		typ,
		"withExec",
		WithArgs(NewArgument("execMD", NewLiteralDigestedString(`{"clientID":"b","execID":"2"}`, execMDDigest), false)),
	)

	if idA.Digest() != idB.Digest() {
		t.Fatalf("expected digested-string payload changes to not affect recipe digest: %s vs %s", idA.Digest(), idB.Digest())
	}
}

func TestDigestedStringDigestDistinguishesIdentity(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	digestA := digest.FromString("execmd-identity-a")
	digestB := digest.FromString("execmd-identity-b")

	idA := New().Append(
		typ,
		"withExec",
		WithArgs(NewArgument("execMD", NewLiteralDigestedString(`{"same":"payload"}`, digestA), false)),
	)
	idB := New().Append(
		typ,
		"withExec",
		WithArgs(NewArgument("execMD", NewLiteralDigestedString(`{"same":"payload"}`, digestB), false)),
	)

	if idA.Digest() == idB.Digest() {
		t.Fatalf("expected different recipe digests for different digested-string digests: %s", idA.Digest())
	}
}

func TestDigestedStringRoundTrip(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	execMDDigest := digest.FromString("execmd-roundtrip")
	orig := New().Append(
		typ,
		"withExec",
		WithArgs(NewArgument("execMD", NewLiteralDigestedString(`{"nested":true}`, execMDDigest), false)),
	)

	enc, err := orig.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded := new(ID)
	if err := decoded.Decode(enc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	arg := decoded.Arg("execMD")
	if arg == nil {
		t.Fatalf("decoded execMD arg missing")
	}

	lit, ok := arg.Value().(*LiteralDigestedString)
	if !ok {
		t.Fatalf("unexpected literal type after decode: %T", arg.Value())
	}
	if lit.Digest() != execMDDigest {
		t.Fatalf("digested-string digest mismatch after round-trip: got %s, want %s", lit.Digest(), execMDDigest)
	}
	if lit.Value() != `{"nested":true}` {
		t.Fatalf("digested-string value mismatch after round-trip: got %q", lit.Value())
	}
}
