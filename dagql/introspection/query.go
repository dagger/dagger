package introspection

import _ "embed"

//go:embed query.graphql
var Query string

// Response is the introspection query response
type Response struct {
	Schema        *ResponseSchema `json:"__schema"`
	SchemaVersion string          `json:"__schemaVersion"`
}

type ResponseSchema struct {
	QueryType        ResponseNamedType   `json:"queryType"`
	MutationType     *ResponseNamedType  `json:"mutationType,omitempty"`
	SubscriptionType *ResponseNamedType  `json:"subscriptionType,omitempty"`
	Types            ResponseSchemaTypes `json:"types"`
	// Interfaces    ResponseSchemaTypes `json:"interfaces"`
	// PossibleTypes ResponseSchemaTypes `json:"possibleTypes"`
	Directives []ResponseDirective `json:"directives"`
}

type ResponseNamedType struct {
	Name string `json:"name"`
}

type ResponseDirective struct {
	Name         string                      `json:"name"`
	Description  string                      `json:"description"`
	Locations    []ResponseDirectiveLocation `json:"locations"`
	Args         []ResponseInputValue        `json:"args"`
	IsRepeatable bool                        `json:"isRepeatable"`
}

type ResponseDirectiveLocation string

func (s *ResponseSchema) Query() *ResponseType {
	return s.Types.Get(s.QueryType.Name)
}

func (s *ResponseSchema) Mutation() *ResponseType {
	return s.Types.Get(s.MutationType.Name)
}

func (s *ResponseSchema) Subscription() *ResponseType {
	return s.Types.Get(s.SubscriptionType.Name)
}

// func (s *SchemaResponse) Visit(handlers VisitHandlers) error {
// 	v := Visitor{
// 		schema:   s,
// 		handlers: handlers,
// 	}
// 	return v.Run()
// }

type ResponseTypeKind string

const (
	ResponseTypeKindScalar      = ResponseTypeKind("SCALAR")
	ResponseTypeKindObject      = ResponseTypeKind("OBJECT")
	ResponseTypeKindInterface   = ResponseTypeKind("INTERFACE")
	ResponseTypeKindUnion       = ResponseTypeKind("UNION")
	ResponseTypeKindEnum        = ResponseTypeKind("ENUM")
	ResponseTypeKindInputObject = ResponseTypeKind("INPUT_OBJECT")
	ResponseTypeKindList        = ResponseTypeKind("LIST")
	ResponseTypeKindNonNull     = ResponseTypeKind("NON_NULL")
)

type Scalar string

const (
	ScalarInt     = Scalar("Int")
	ScalarFloat   = Scalar("Float")
	ScalarString  = Scalar("String")
	ScalarBoolean = Scalar("Boolean")
)

type ResponseType struct {
	Kind          ResponseTypeKind     `json:"kind"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Fields        []*ResponseField     `json:"fields,omitempty"`
	InputFields   []ResponseInputValue `json:"inputFields,omitempty"`
	EnumValues    []ResponseEnumValue  `json:"enumValues,omitempty"`
	Interfaces    ResponseSchemaTypes  `json:"interfaces,omitempty"`
	PossibleTypes ResponseSchemaTypes  `json:"possibleTypes,omitempty"`
}

type ResponseSchemaTypes []*ResponseType

func (t ResponseSchemaTypes) Get(name string) *ResponseType {
	for _, i := range t {
		if i.Name == name {
			return i
		}
	}
	return nil
}

type ResponseField struct {
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	TypeRef           *ResponseTypeRef    `json:"type"`
	Args              ResponseInputValues `json:"args"`
	IsDeprecated      bool                `json:"isDeprecated"`
	DeprecationReason string              `json:"deprecationReason"`

	ParentObject *Type `json:"-"`
}

type ResponseTypeRef struct {
	Kind   ResponseTypeKind `json:"kind"`
	Name   string           `json:"name,omitempty"`
	OfType *ResponseTypeRef `json:"ofType,omitempty"`
}

func (r ResponseTypeRef) IsOptional() bool {
	return r.Kind != ResponseTypeKindNonNull
}

func (r ResponseTypeRef) IsScalar() bool {
	ref := r
	if r.Kind == ResponseTypeKindNonNull {
		ref = *ref.OfType
	}
	if ref.Kind == ResponseTypeKindScalar {
		return true
	}
	if ref.Kind == ResponseTypeKindEnum {
		return true
	}
	return false
}

func (r ResponseTypeRef) IsObject() bool {
	ref := r
	if r.Kind == ResponseTypeKindNonNull {
		ref = *ref.OfType
	}
	if ref.Kind == ResponseTypeKindObject {
		return true
	}
	return false
}

func (r ResponseTypeRef) IsList() bool {
	ref := r
	if r.Kind == ResponseTypeKindNonNull {
		ref = *ref.OfType
	}
	if ref.Kind == ResponseTypeKindList {
		return true
	}
	return false
}

type ResponseInputValues []ResponseInputValue

func (i ResponseInputValues) HasOptionals() bool {
	for _, v := range i {
		if v.TypeRef.IsOptional() {
			return true
		}
	}
	return false
}

type ResponseInputValue struct {
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	DefaultValue      *string          `json:"defaultValue"`
	TypeRef           *ResponseTypeRef `json:"type"`
	IsDeprecated      bool             `json:"isDeprecated"`
	DeprecationReason string           `json:"deprecationReason"`
}

type ResponseEnumValue struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	IsDeprecated      bool   `json:"isDeprecated"`
	DeprecationReason string `json:"deprecationReason"`
}
