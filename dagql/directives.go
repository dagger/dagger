package dagql

import (
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

type DirectiveSpec struct {
	Name         string              `field:"true"`
	Description  string              `field:"true"`
	Args         InputSpecs          `field:"true"`
	Locations    []DirectiveLocation `field:"true"`
	IsRepeatable bool                `field:"true"`
}

type DirectiveLocation string

func (DirectiveLocation) Type() *ast.Type {
	return &ast.Type{
		NamedType: "DirectiveLocation",
		NonNull:   true,
	}
}

func (d DirectiveSpec) DirectiveDefinition() *ast.DirectiveDefinition {
	def := &ast.DirectiveDefinition{
		Name:         d.Name,
		Description:  d.Description,
		Arguments:    d.Args.ArgumentDefinitions(),
		IsRepeatable: d.IsRepeatable,
	}
	for _, loc := range d.Locations {
		def.Locations = append(def.Locations, ast.DirectiveLocation(loc))
	}
	return def
}

var _ Input = DirectiveLocation("")

func (DirectiveLocation) Decoder() InputDecoder {
	return DirectiveLocations
}

func (d DirectiveLocation) ToLiteral() *idproto.Literal {
	return DirectiveLocations.Literal(d)
}

var DirectiveLocations = NewEnum[DirectiveLocation]()

var (
	DirectiveLocationQuery                = DirectiveLocations.Register("QUERY")
	DirectiveLocationMutation             = DirectiveLocations.Register("MUTATION")
	DirectiveLocationSubscription         = DirectiveLocations.Register("SUBSCRIPTION")
	DirectiveLocationField                = DirectiveLocations.Register("FIELD")
	DirectiveLocationFragmentDefinition   = DirectiveLocations.Register("FRAGMENT_DEFINITION")
	DirectiveLocationFragmentSpread       = DirectiveLocations.Register("FRAGMENT_SPREAD")
	DirectiveLocationInlineFragment       = DirectiveLocations.Register("INLINE_FRAGMENT")
	DirectiveLocationVariableDefinition   = DirectiveLocations.Register("VARIABLE_DEFINITION")
	DirectiveLocationSchema               = DirectiveLocations.Register("SCHEMA")
	DirectiveLocationScalar               = DirectiveLocations.Register("SCALAR")
	DirectiveLocationObject               = DirectiveLocations.Register("OBJECT")
	DirectiveLocationFieldDefinition      = DirectiveLocations.Register("FIELD_DEFINITION")
	DirectiveLocationArgumentDefinition   = DirectiveLocations.Register("ARGUMENT_DEFINITION")
	DirectiveLocationInterface            = DirectiveLocations.Register("INTERFACE")
	DirectiveLocationUnion                = DirectiveLocations.Register("UNION")
	DirectiveLocationEnum                 = DirectiveLocations.Register("ENUM")
	DirectiveLocationEnumValue            = DirectiveLocations.Register("ENUM_VALUE")
	DirectiveLocationInputObject          = DirectiveLocations.Register("INPUT_OBJECT")
	DirectiveLocationInputFieldDefinition = DirectiveLocations.Register("INPUT_FIELD_DEFINITION")
)

func deprecated(reason string) *ast.Directive {
	return &ast.Directive{
		Name: "deprecated",
		Arguments: []*ast.Argument{
			{
				Name: "reason",
				Value: &ast.Value{
					Kind: ast.EnumValue,
					Raw:  reason,
				},
			},
		},
	}
}

func impure() *ast.Directive {
	return &ast.Directive{
		Name: "impure",
	}
}

func meta() *ast.Directive {
	return &ast.Directive{
		Name: "meta",
	}
}
