package enginefree

import (
	"go/ast"
	"go/token"
	"testing"
)

// The module source and generated bindings are one reviewable interface. Keeping this audit
// engine-free makes a stale binding failure visible before an exact-engine sign-off is attempted.
func TestSignoffGeneratedModuleSurfaceMatchesSource(t *testing.T) {
	t.Parallel()

	adapter := parseGoFile(t, "../../dagger.gen.go")
	for _, function := range []string{"Signoff", "SignoffArtifact"} {
		if got := selectorCount(adapter, function); got != 1 {
			t.Errorf(
				"generated adapter must dispatch %s exactly once, got %d; run the scoped rust-sdk-dev generation after signatures stabilize",
				function,
				got,
			)
		}
		if got := stringLiteralCount(adapter, function); got != 2 {
			t.Errorf(
				"generated adapter must expose and dispatch %s, got %d registrations; run the scoped rust-sdk-dev generation after signatures stabilize",
				function,
				got,
			)
		}
	}
	for _, method := range []string{"MarshalJSON", "UnmarshalJSON"} {
		if !hasMethod(adapter, "RustSignoffArtifact", method) {
			t.Errorf(
				"generated adapter is missing RustSignoffArtifact.%s; run the scoped rust-sdk-dev generation after signatures stabilize",
				method,
			)
		}
	}
	if got := stringLiteralCount(adapter, "RustSignoffArtifact"); got != 2 {
		t.Errorf(
			"generated adapter must retain the RustSignoffArtifact object in dispatch and schema registration, got %d anchors",
			got,
		)
	}
	for _, field := range []string{
		"Bundle", "PlanJSON", "ManifestJSON", "BuildReceiptJSON", "PayloadDigest",
	} {
		if got := stringLiteralCount(adapter, field); got != 1 {
			t.Errorf("generated RustSignoffArtifact must expose public field %s exactly once, got %d", field, got)
		}
	}
	for _, private := range []string{"Payload", "Manifest", "BuildReceipt", "Target", "CLI"} {
		if got := stringLiteralCount(adapter, private); got != 0 {
			t.Errorf("generated RustSignoffArtifact must not register private field %s, got %d", private, got)
		}
	}

	client := parseGoFile(t, "../dagger/rust-sdk-dev.gen.go")
	if !hasType(client, "RustSDKDevRustSignoffArtifact") {
		t.Errorf(
			"generated self-call client is missing RustSDKDevRustSignoffArtifact; run the scoped rust-sdk-dev generation after signatures stabilize",
		)
	}
	for _, method := range []string{"Signoff", "SignoffArtifact"} {
		if !hasMethod(client, "RustSDKDev", method) {
			t.Errorf("generated self-call client is missing RustSDKDev.%s", method)
		}
	}
	for _, method := range []string{
		"Bundle", "PlanJSON", "ManifestJSON", "BuildReceiptJSON", "PayloadDigest",
	} {
		if !hasMethod(client, "RustSDKDevRustSignoffArtifact", method) {
			t.Errorf("generated self-call client is missing public RustSignoffArtifact getter %s", method)
		}
	}
	for _, private := range []string{"Payload", "Manifest", "BuildReceipt", "Target", "CLI"} {
		if hasMethod(client, "RustSDKDevRustSignoffArtifact", private) {
			t.Errorf("generated self-call client must hide private RustSignoffArtifact getter %s", private)
		}
	}
	for _, selector := range []string{"signoff", "signoffArtifact", "buildReceiptJson"} {
		if got := stringLiteralCount(client, selector); got != 1 {
			t.Errorf("generated self-call client must select %q exactly once, got %d", selector, got)
		}
	}
}

func hasMethod(file *ast.File, receiver, name string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != name || len(function.Recv.List) != 1 {
			continue
		}
		if receiverTypeName(function.Recv.List[0].Type) == receiver {
			return true
		}
	}
	return false
}

func receiverTypeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return receiverTypeName(expression.X)
	default:
		return ""
	}
}

func hasType(file *ast.File, name string) bool {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if ok && typeSpecification.Name.Name == name {
				return true
			}
		}
	}
	return false
}
