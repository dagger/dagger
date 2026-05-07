package main

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestFormatTypeRendersDirectiveApplications(t *testing.T) {
	idRef := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "ID"}
	stringRef := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}

	tests := []struct {
		name string
		typ  *introspection.Type
		want string
	}{
		{
			name: "object type fields and field arguments",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindObject,
				Name:       "Obj",
				Directives: directives(directive("objectDirective", directiveArg("name", `"Obj"`))),
				Fields: []*introspection.Field{
					{
						Name:    "field",
						TypeRef: idRef,
						Args: introspection.InputValues{
							{
								Name:         "arg",
								TypeRef:      stringRef,
								DefaultValue: strPtr(`"default"`),
								Directives:   directives(directive("expectedType", directiveArg("name", `"ArgType"`))),
							},
						},
						Directives: directives(directive("expectedType", directiveArg("name", `"Obj"`))),
					},
				},
			},
			want: "type Obj @objectDirective(name: \"Obj\") {\n" +
				"  field(arg: String = \"default\" @expectedType(name: \"ArgType\")): ID @expectedType(name: \"Obj\")\n" +
				"}\n",
		},
		{
			name: "input object and input fields",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindInputObject,
				Name:       "Input",
				Directives: directives(directive("inputDirective")),
				InputFields: introspection.InputValues{
					{
						Name:         "value",
						TypeRef:      stringRef,
						DefaultValue: strPtr(`"default"`),
						Directives:   directives(directive("expectedType", directiveArg("name", `"InputValue"`))),
					},
				},
			},
			want: "input Input @inputDirective {\n" +
				"  value: String = \"default\" @expectedType(name: \"InputValue\")\n" +
				"}\n",
		},
		{
			name: "enum and enum values",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindEnum,
				Name:       "Choice",
				Directives: directives(directive("enumDirective")),
				EnumValues: []introspection.EnumValue{
					{
						Name:       "FIRST",
						Directives: directives(directive("enumValue", directiveArg("value", `"first"`))),
					},
				},
			},
			want: "enum Choice @enumDirective {\n" +
				"  FIRST @enumValue(value: \"first\")\n" +
				"}\n",
		},
		{
			name: "scalar",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindScalar,
				Name:       "CustomScalar",
				Directives: directives(directive("scalarDirective")),
			},
			want: "scalar CustomScalar @scalarDirective\n",
		},
		{
			name: "interface",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindInterface,
				Name:       "Iface",
				Directives: directives(directive("interfaceDirective")),
				Fields: []*introspection.Field{
					{Name: "id", TypeRef: idRef},
				},
			},
			want: "interface Iface @interfaceDirective {\n" +
				"  id: ID\n" +
				"}\n",
		},
		{
			name: "union",
			typ: &introspection.Type{
				Kind:       introspection.TypeKindUnion,
				Name:       "SearchResult",
				Directives: directives(directive("unionDirective")),
				PossibleTypes: []*introspection.Type{
					{Kind: introspection.TypeKindObject, Name: "File"},
					{Kind: introspection.TypeKindObject, Name: "Directory"},
				},
			},
			want: "union SearchResult @unionDirective = File | Directory\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got strings.Builder
			formatType(&got, tt.typ)
			if got.String() != tt.want {
				t.Fatalf("unexpected schema:\n%s\nwant:\n%s", got.String(), tt.want)
			}
		})
	}
}

func TestFormatDirectiveRendersArgumentDirectives(t *testing.T) {
	var got strings.Builder
	formatDirective(&got, &introspection.DirectiveDef{
		Name: "customDirective",
		Args: introspection.InputValues{
			{
				Name:       "arg",
				TypeRef:    &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"},
				Directives: directives(directive("deprecated", directiveArg("reason", `"because"`))),
			},
		},
		Locations: []string{"FIELD_DEFINITION"},
	})

	want := "directive @customDirective(arg: String @deprecated(reason: \"because\")) on FIELD_DEFINITION\n"
	if got.String() != want {
		t.Fatalf("unexpected directive definition:\n%s\nwant:\n%s", got.String(), want)
	}
}

func directives(directives ...*introspection.Directive) introspection.Directives {
	return introspection.Directives(directives)
}

func directive(name string, args ...*introspection.DirectiveArg) *introspection.Directive {
	return &introspection.Directive{Name: name, Args: args}
}

func directiveArg(name, value string) *introspection.DirectiveArg {
	return &introspection.DirectiveArg{Name: name, Value: strPtr(value)}
}

func strPtr(s string) *string {
	return &s
}
