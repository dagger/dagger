package call

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/util/hashutil"
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

func TestModuleIdentityContributesAsSyntheticInput(t *testing.T) {
	typ := &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
	moduleAID := New().Append(typ, "moduleA")
	moduleBID := New().Append(typ, "moduleB")
	idNoModule := New().Append(typ, "field")
	idWithModuleA := idNoModule.With(WithModule(NewModule(moduleAID, "mod", "ref", "pin")))
	idWithModuleB := idNoModule.With(WithModule(NewModule(moduleBID, "mod", "ref", "pin")))

	if idNoModule.Digest() == idWithModuleA.Digest() {
		t.Fatalf("expected module-scoped digest to differ from unscoped digest: %s", idWithModuleA.Digest())
	}
	if idWithModuleA.Digest() == idWithModuleB.Digest() {
		t.Fatalf("different module IDs should produce different digests: %s", idWithModuleA.Digest())
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
		t.Fatalf("module synthetic input should append one input digest: %d vs %d", len(inputsNoModule), len(inputsModuleA))
	}
	if len(inputsModuleA) != len(inputsModuleB) {
		t.Fatalf("module input count mismatch: %d vs %d", len(inputsModuleA), len(inputsModuleB))
	}
	last := len(inputsModuleA) - 1
	if inputsModuleA[last] != moduleAID.inputDigest() {
		t.Fatalf("unexpected moduleA synthetic input digest: got %s, want %s", inputsModuleA[last], moduleAID.inputDigest())
	}
	if inputsModuleB[last] != moduleBID.inputDigest() {
		t.Fatalf("unexpected moduleB synthetic input digest: got %s, want %s", inputsModuleB[last], moduleBID.inputDigest())
	}
}

func TestDagOpDigestKeepsSelfShapeWhenContentMatches(t *testing.T) {
	commonContent := digest.FromString("shared-content")
	idA := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "fieldA").With(WithContentDigest(commonContent))
	idB := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "fieldB").With(WithContentDigest(commonContent))

	if idA.OutputEquivalentDigest() != idB.OutputEquivalentDigest() {
		t.Fatalf("expected matching output-equivalent digest: %s vs %s", idA.OutputEquivalentDigest(), idB.OutputEquivalentDigest())
	}
	if idA.DagOpDigest() == idB.DagOpDigest() {
		t.Fatalf("expected dag-op digest to differ for different call self shape: %s", idA.DagOpDigest())
	}
}

func TestDagOpDigestMatchesSelfPlusDagOpInputsHash(t *testing.T) {
	receiver := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "receiver").With(WithContentDigest(digest.FromString("receiver-content")))
	argID := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "arg").With(WithContentDigest(digest.FromString("arg-content")))
	id := receiver.Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "child",
		WithArgs(NewArgument("idArg", NewLiteralID(argID), false)),
		WithImplicitInputs(NewArgument("scope", NewLiteralString("scope-a"), false)),
	)

	selfDigest, inputDigests, err := id.DagOpSelfDigestAndInputs()
	if err != nil {
		t.Fatalf("self+dag-op-input digests: %v", err)
	}
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputDigests {
		h = h.WithString(in.String())
	}
	expected := digest.Digest(h.DigestAndClose())

	if got := id.DagOpDigest(); got != expected {
		t.Fatalf("unexpected dag-op digest: got %s, want %s", got, expected)
	}
}

func TestSelfDigestAndInputsIgnoreAuxiliaryExtraDigests(t *testing.T) {
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
			Kind:   ExtraDigestKindAuxiliary,
		}))
	recvB := New().Append(typ, "receiver").
		With(WithContentDigest(sharedContent)).
		With(WithExtraDigest(ExtraDigest{
			Digest: auxB,
			Label:  "aux",
			Kind:   ExtraDigestKindAuxiliary,
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
		t.Fatalf("self digest should match when only auxiliary digests differ: %s vs %s", selfA, selfB)
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

func TestExtraDigestKindsDefaultForLegacyUnspecifiedEntries(t *testing.T) {
	contentDigest := digest.FromString("legacy-content")
	customDigest := digest.FromString("legacy-custom")
	additionalDigest := digest.FromString("legacy-additional")

	id := &ID{
		pb: &callpbv1.Call{
			ExtraDigests: []*callpbv1.ExtraDigest{
				{Digest: contentDigest.String(), Label: "content"},
				{Digest: customDigest.String(), Label: "custom"},
				{Digest: additionalDigest.String(), Label: ""},
			},
		},
	}

	extras := id.ExtraDigests()
	if len(extras) != 3 {
		t.Fatalf("expected 3 extra digests, got %d", len(extras))
	}

	byDigest := map[digest.Digest]ExtraDigest{}
	for _, extra := range extras {
		byDigest[extra.Digest] = extra
	}

	if got := byDigest[contentDigest].Kind; got != ExtraDigestKindOutputEquivalence {
		t.Fatalf("content digest should default to output-equivalence kind, got %s", got)
	}
	if got := byDigest[customDigest].Kind; got != ExtraDigestKindAuxiliary {
		t.Fatalf("custom digest should default to auxiliary kind, got %s", got)
	}
	if got := byDigest[additionalDigest].Kind; got != ExtraDigestKindOutputEquivalence {
		t.Fatalf("additional digest should default to output-equivalence kind, got %s", got)
	}
}

func TestExtraDigestKindsRoundTripThroughProto(t *testing.T) {
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
			Kind:   ExtraDigestKindAuxiliary,
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
	if extras[0].Kind != ExtraDigestKindAuxiliary {
		t.Fatalf("kind mismatch: got %s, want %s", extras[0].Kind, ExtraDigestKindAuxiliary)
	}
}
