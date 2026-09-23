package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

func TestClassFieldDeclarationOrder(t *testing.T) {
	srv := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
	class := NewClass[*reflectedAccessorTestObject](srv)
	field := func(name string, view ViewFilter) Field[*reflectedAccessorTestObject] {
		return Field[*reflectedAccessorTestObject]{Spec: &FieldSpec{
			Name: name, Type: String(""), ViewFilter: view,
		}}
	}
	class.Install(field("zebra", AllView{}), field("hidden", ExactView("future")), field("alpha", nil))
	// Installing through a value copy must update the original class's order.
	copy := class
	copy.Install(field("middle", nil), field("zebra", ExactView("future")))
	class.Extend(FieldSpec{Name: "last", Type: String("")}, nil)

	check := func(obj ObjectType, view call.View, want []string) {
		t.Helper()
		require.Equal(t, want, definitionFieldNames(obj.(Definitive).TypeDefinition(view)))
		var names []string
		for _, spec := range obj.FieldSpecs(view) {
			names = append(names, spec.Name)
		}
		require.Equal(t, want, names)
	}
	check(class, "", []string{"id", "zebra", "alpha", "middle", "last"})
	check(class, "future", []string{"id", "zebra", "hidden", "alpha", "middle", "last"})

	forked, err := class.ForkObjectType(srv)
	require.NoError(t, err)
	forked.Extend(FieldSpec{Name: "forkOnly", Type: String("")}, nil)
	class.Install(field("originalOnly", nil))
	check(forked, "", []string{"id", "zebra", "alpha", "middle", "last", "forkOnly"})
	check(class, "", []string{"id", "zebra", "alpha", "middle", "last", "originalOnly"})
}

func TestInterfaceFieldDeclarationOrder(t *testing.T) {
	iface := NewInterface("Ordered", "")
	add := func(name string, version call.View) {
		iface.AddField(InterfaceFieldSpec{
			FieldSpec:  FieldSpec{Name: name, Type: String("")},
			MinVersion: version,
		})
	}
	add("zebra", "")
	add("hidden", "future")
	add("alpha", "")
	add("zebra", "future")
	require.Equal(t, []string{"zebra", "alpha"}, definitionFieldNames(iface.Definition("")))
	require.Equal(t, []string{"zebra", "hidden", "alpha"}, definitionFieldNames(iface.Definition("future")))
}

func definitionFieldNames(def *ast.Definition) []string {
	var names []string
	for _, field := range def.Fields {
		names = append(names, field.Name)
	}
	return names
}
