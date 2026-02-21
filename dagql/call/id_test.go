package call

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

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

	selfA, inputsA, err := idA.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("idA self+inputs: %v", err)
	}
	selfB, inputsB, err := idB.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("idB self+inputs: %v", err)
	}

	if selfA != selfB {
		t.Fatalf("module metadata should not affect self digest: %s vs %s", selfA, selfB)
	}
	if len(inputsA) != len(inputsB) {
		t.Fatalf("input count mismatch: %d vs %d", len(inputsA), len(inputsB))
	}
	for i := range inputsA {
		if inputsA[i] != inputsB[i] {
			t.Fatalf("input digest mismatch at %d: %s vs %s", i, inputsA[i], inputsB[i])
		}
	}
}

func TestModuleIdentityIsImplicitInputOnly(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	sharedModuleContent := digest.FromString("module-content")
	moduleAID := New().Append(typ, "moduleA").With(WithContentDigest(sharedModuleContent))
	moduleBID := New().Append(typ, "moduleB").With(WithContentDigest(sharedModuleContent))

	// Reality/model:
	// 1. module recipe identity contributes to call recipe digest
	// 2. module identity is also represented as an implicit input in SelfDigestAndInputs
	idNoModule := New().Append(typ, "field")
	idWithModuleA := idNoModule.With(WithModule(NewModule(moduleAID, "mod", "ref", "pin")))
	idWithModuleB := idNoModule.With(WithModule(NewModule(moduleBID, "mod", "ref", "pin")))

	if idNoModule.Digest() == idWithModuleA.Digest() {
		t.Fatalf("module identity should affect recipe digest: %s vs %s", idNoModule.Digest(), idWithModuleA.Digest())
	}
	if idWithModuleA.Digest() == idWithModuleB.Digest() {
		t.Fatalf("distinct module identities should affect recipe digest: %s vs %s", idWithModuleA.Digest(), idWithModuleB.Digest())
	}

	selfNoModule, inputsNoModule, err := idNoModule.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("no-module self+inputs: %v", err)
	}
	selfModuleA, inputsModuleA, err := idWithModuleA.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("moduleA self+inputs: %v", err)
	}
	selfModuleB, inputsModuleB, err := idWithModuleB.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("moduleB self+inputs: %v", err)
	}

	if selfNoModule != selfModuleA {
		t.Fatalf("module identity should not affect self digest")
	}
	if selfNoModule != selfModuleB {
		t.Fatalf("module identity should not affect self digest: %s vs %s", selfNoModule, selfModuleB)
	}

	if len(inputsModuleA) != len(inputsNoModule)+1 {
		t.Fatalf("module implicit input should append one input digest: %d vs %d", len(inputsNoModule), len(inputsModuleA))
	}
	if len(inputsModuleA) != len(inputsModuleB) {
		t.Fatalf("module input count mismatch: %d vs %d", len(inputsModuleA), len(inputsModuleB))
	}
	last := len(inputsModuleA) - 1
	if inputsModuleA[last] != moduleAID.Digest() {
		t.Fatalf("unexpected moduleA implicit input digest: got %s, want %s", inputsModuleA[last], moduleAID.Digest())
	}
	if inputsModuleB[last] != moduleBID.Digest() {
		t.Fatalf("unexpected moduleB implicit input digest: got %s, want %s", inputsModuleB[last], moduleBID.Digest())
	}
	if inputsModuleA[last] == inputsModuleB[last] {
		t.Fatalf("module implicit input should reflect module recipe identity: %s vs %s", inputsModuleA[last], inputsModuleB[last])
	}
}

func TestSelfDigestAndInputsUseRecipeDigestsForIDInputs(t *testing.T) {
	sharedContent := digest.FromString("shared-output")
	auxA := digest.FromString("aux-a")
	auxB := digest.FromString("aux-b")
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}

	recvA := New().Append(typ, "receiver").
		With(WithContentDigest(sharedContent)).
		With(WithExtraDigest(ExtraDigest{
			Digest: auxA,
			Label:  "aux",
		}))
	recvB := New().Append(typ, "receiver").
		With(WithContentDigest(sharedContent)).
		With(WithExtraDigest(ExtraDigest{
			Digest: auxB,
			Label:  "aux",
		}))

	childA := recvA.Append(typ, "child")
	childB := recvB.Append(typ, "child")

	selfA, inputsA, err := childA.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("childA self+inputs: %v", err)
	}
	selfB, inputsB, err := childB.SelfDigestAndInputs()
	if err != nil {
		t.Fatalf("childB self+inputs: %v", err)
	}

	if selfA != selfB {
		t.Fatalf("self digest should match when only extra digests differ: %s vs %s", selfA, selfB)
	}
	if len(inputsA) != len(inputsB) {
		t.Fatalf("input count mismatch: %d vs %d", len(inputsA), len(inputsB))
	}
	for i := range inputsA {
		if inputsA[i] != inputsB[i] {
			t.Fatalf("expected recipe-only input digests to match at %d: %s vs %s", i, inputsA[i], inputsB[i])
		}
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
