package generator

import "github.com/dagger/dagger/codegen/introspection"

var _schema *introspection.Schema

func SetSchema(schema *introspection.Schema) {
	_schema = schema
}

func GetSchema() *introspection.Schema {
	return _schema
}
