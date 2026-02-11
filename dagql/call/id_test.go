package call

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
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
