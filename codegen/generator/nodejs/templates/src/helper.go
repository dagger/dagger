package test

import (
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/nodejs/templates"
)

func templateHelper(t *testing.T) *template.Template {
	t.Helper()
	return templates.New()
}
