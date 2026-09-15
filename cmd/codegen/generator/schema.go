package generator

import (
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

var _schema *introspection.Schema

func SetSchema(schema *introspection.Schema) {
	_schema = schema
}

func GetSchema() *introspection.Schema {
	return _schema
}

// SetSchemaParents sets all the parents for the fields.
func SetSchemaParents(schema *introspection.Schema) {
	for _, t := range schema.Types {
		for _, f := range t.Fields {
			f.ParentObject = t
		}
	}
}
