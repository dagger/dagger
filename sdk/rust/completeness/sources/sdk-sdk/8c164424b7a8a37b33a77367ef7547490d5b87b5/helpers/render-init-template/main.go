package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iancoleman/strcase"
)

var dangTypeNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: render-init-template MODULE_NAME TEMPLATE_FILE OUT_FILE")
	}

	moduleName := args[0]
	moduleType := strcase.ToCamel(moduleName)
	if !dangTypeNamePattern.MatchString(moduleType) {
		return fmt.Errorf("name must produce a valid Dang root type, for example my-sdk")
	}

	templatePath := args[1]
	outPath := args[2]

	contents, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}

	rendered := strings.ReplaceAll(string(contents), "__SDK_NAME__", moduleType)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(rendered), 0o644)
}
