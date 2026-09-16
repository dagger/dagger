package main

//go:generate:include ../../docs/static/reference/dagger.schema.json
//go:generate:include ../../docs/static/reference/dagger-module.schema.json
//go:generate:include ../../docs/static/reference/dagger-workspace.schema.json

//go:generate go -C ../.. run ./internal/jsonschema
