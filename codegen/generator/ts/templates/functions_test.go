package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatName(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"fooId -> fooId":           {"fooId", "fooId"},
		"containers -> containers": {"containers", "containers"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := FormatName(c.in)
			require.Equal(t, c.want, got)
		})
	}
}

func TestFormatType(t *testing.T) {
	t.Run("String -> string", func(t *testing.T) {
		// TODO
		// introspection.Field
		// got := FormatInputType("String!")
		// require.Equal(t, "string", got)
	})
}

// func TestFieldFunction(t *testing.T) {
// 	args := introspection.InputValues{
// 		introspection.InputValue{"path", "path desc", nil, nil},
// 	}
// 	cases := map[string]struct {
// 		name     string
// 		kind     string
// 		typeName string
// 		args     *introspection.InputValues
//
// 		want string
// 	}{
// 		"contents: String! -> contents() string":                     {"contents", "INTERFACE", "String!", nil, "contents() string"},
// 		"containers: Container -> containers() Container":            {"containers", "SCALAR", "container", nil, "containers() container"},
// 		"containers: Container -> containers(string path) Container": {"containers", "SCALAR", "container", &args, "containers(string path) container"},
// 	}
//
// 	for name, c := range cases {
// 		t.Run(name, func(t *testing.T) {
// 			f := introspection.Field{
// 				Name: c.name,
// 				TypeRef: &introspection.TypeRef{
// 					Kind: introspection.TypeKind(c.kind),
// 					Name: c.typeName,
// 					// OfType?
// 				},
// 				//Args: introspection.InputValues{
// 				//	{Name: },
// 				//},
// 			}
// 			got := fieldFunction(f)
// 			require.Equal(t, c.want, got)
// 		})
// 	}
//
// }
