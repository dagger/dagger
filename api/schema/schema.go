package schema

import (
	"embed"
	"io/fs"
)

//go:embed *.graphql
var schemas embed.FS

var Schema string
var Operations string

func init() {
	var (
		err  error
		data []byte
	)

	data, err = fs.ReadFile(schemas, "schema.graphql")
	if err != nil {
		panic(err)
	}
	Schema = string(data)

	data, err = fs.ReadFile(schemas, "operations.graphql")
	if err != nil {
		panic(err)
	}
	Operations = string(data)
}
