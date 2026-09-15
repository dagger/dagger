package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

type reflectedAccessorTestObject struct {
	Value int `field:"true"`
}

func (*reflectedAccessorTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: "ReflectedAccessorTestObject", NonNull: true}
}

func TestFieldsInstallMarksReflectedAccessorsTrivial(t *testing.T) {
	srv := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
	Fields[*reflectedAccessorTestObject]{}.Install(srv)

	obj, ok := srv.ObjectType("ReflectedAccessorTestObject")
	require.True(t, ok)
	spec, ok := obj.FieldSpec("value", call.View(""))
	require.True(t, ok)
	require.True(t, spec.Trivial)
	require.False(t, spec.NoTelemetry)
}
