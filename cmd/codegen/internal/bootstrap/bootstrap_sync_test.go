package bootstrap_test

import (
	"testing"

	"github.com/dagger/dagger/cmd/codegen/internal/bootstrap"
)

func TestBootstrapTypes(t *testing.T) {
	req := &bootstrap.Request{
		Query: "{ version }",
	}
	if req.Query != "{ version }" {
		t.Errorf("expected query { version }, got %s", req.Query)
	}

	var id bootstrap.ID = "test-id-123"
	if string(id) != "test-id-123" {
		t.Errorf("expected test-id-123, got %s", string(id))
	}
}

func TestBootstrapTypeDefKinds(t *testing.T) {
	kinds := []bootstrap.TypeDefKind{
		bootstrap.TypeDefKindStringKind,
		bootstrap.TypeDefKindIntegerKind,
		bootstrap.TypeDefKindBooleanKind,
		bootstrap.TypeDefKindFloatKind,
		bootstrap.TypeDefKindVoidKind,
		bootstrap.TypeDefKindListKind,
		bootstrap.TypeDefKindObjectKind,
		bootstrap.TypeDefKindInterfaceKind,
		bootstrap.TypeDefKindEnumKind,
		bootstrap.TypeDefKindScalarKind,
	}

	for _, k := range kinds {
		if k == "" {
			t.Error("TypeDefKind constant should not be empty")
		}
	}
}
