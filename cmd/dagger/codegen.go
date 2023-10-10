package main

import (
	"github.com/spf13/pflag"
)

var (
	codegenFlags     = pflag.NewFlagSet("codegen", pflag.ContinueOnError)
	codegenOutputDir string
)

func init() {
	codegenFlags.StringVarP(&codegenOutputDir, "output", "o", ".", "output directory")
}
