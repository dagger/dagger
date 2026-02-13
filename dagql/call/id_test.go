package call

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

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

func TestStructuralEquivalentDigestKeepsSelfShapeWhenContentMatches(t *testing.T) {
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
	if idA.StructuralEquivalentDigest() == idB.StructuralEquivalentDigest() {
		t.Fatalf("expected structural-equivalent digest to differ for different call self shape: %s", idA.StructuralEquivalentDigest())
	}
}

func TestStructuralEquivalentDigestMatchesSelfPlusEquivalentInputsHash(t *testing.T) {
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

	selfDigest, inputDigests, err := id.SelfDigestAndEquivalentInputs()
	if err != nil {
		t.Fatalf("self+equivalent-input digests: %v", err)
	}
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputDigests {
		h = h.WithString(in.String())
	}
	expected := digest.Digest(h.DigestAndClose())

	if got := id.StructuralEquivalentDigest(); got != expected {
		t.Fatalf("unexpected structural-equivalent digest: got %s, want %s", got, expected)
	}
}

func TestEquivalentDigestAliasesOutputEquivalentDigest(t *testing.T) {
	id := New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "field").With(WithContentDigest(digest.FromString("field-content")))

	if id.EquivalentDigest() != id.OutputEquivalentDigest() {
		t.Fatalf("EquivalentDigest should alias OutputEquivalentDigest: %s vs %s", id.EquivalentDigest(), id.OutputEquivalentDigest())
	}
}
