// sdl-diff compares GraphQL SDL files or Git revision:path objects.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
)

func main() {
	descriptions := flag.Bool("descriptions", false, "include SDL descriptions in the diff")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: sdl-diff [flags] OLD NEW\n\nInputs are SDL files or Git revision:path objects.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), *descriptions); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(old, new string, descriptions bool) error {
	var documents [2]*ast.SchemaDocument
	for i, name := range []string{old, new} {
		data, err := os.ReadFile(name)
		if os.IsNotExist(err) && strings.Contains(name, ":") {
			cmd := exec.Command("git", "show", "--end-of-options", name)
			cmd.Stderr = os.Stderr
			data, err = cmd.Output()
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		documents[i], err = parseDocument(name, string(data), descriptions)
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(os.Stdout, semanticDiff(documents[0], documents[1]))
	return err
}

func normalize(name, input string, descriptions bool) (string, error) {
	doc, err := parseDocument(name, input, descriptions)
	if err != nil {
		return "", err
	}
	return format(doc), nil
}

func parseDocument(name, input string, descriptions bool) (*ast.SchemaDocument, error) {
	doc, err := parser.ParseSchema(&ast.Source{Name: name, Input: input})
	if err != nil {
		return nil, err
	}
	if !descriptions {
		// Clear descriptions before formatting: argument descriptions also affect
		// comma placement in gqlparser's formatter.
		for _, defs := range []ast.SchemaDefinitionList{doc.Schema, doc.SchemaExtension} {
			for _, def := range defs {
				def.Description = ""
			}
		}
		for _, def := range doc.Directives {
			def.Description = ""
			clearArgumentDescriptions(def.Arguments)
		}
		for _, defs := range []ast.DefinitionList{doc.Definitions, doc.Extensions} {
			for _, def := range defs {
				def.Description = ""
				for _, field := range def.Fields {
					field.Description = ""
					clearArgumentDescriptions(field.Arguments)
				}
				for _, value := range def.EnumValues {
					value.Description = ""
				}
			}
		}
	}
	return doc, nil
}

func format(doc *ast.SchemaDocument) string {
	var out strings.Builder
	// Include every explicitly declared field, including internal __ fields.
	formatter.NewFormatter(&out, formatter.WithIndent("  "), formatter.WithBuiltin()).FormatSchemaDocument(doc)
	return out.String()
}

func clearArgumentDescriptions(args ast.ArgumentDefinitionList) {
	for _, arg := range args {
		arg.Description = ""
	}
}
