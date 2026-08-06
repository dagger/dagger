// Command go-authority-extractor deterministically parses an explicit in-memory source bundle.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	var paths pathFlags
	root := flag.String("root", "", "repository root containing the selected Go sources")
	registry := flag.String("registry", "", "canonical authority registry containing the selected paths")
	authority := flag.String("authority", "", "authority ID whose path includes are selected")
	versionLiteral := flag.String("version-literal", "", "string literal that pins the Go SDK revision")
	flag.Var(&paths, "path", "repository-relative Go file or directory to include; repeatable")
	flag.Parse()

	if len(paths) > 0 || *root != "" || *registry != "" || *authority != "" || *versionLiteral != "" {
		if *registry != "" || *authority != "" {
			if *registry == "" || *authority == "" || len(paths) != 0 {
				fmt.Fprintln(os.Stderr, "extract: --registry and --authority must be supplied together and cannot be combined with --path")
				os.Exit(1)
			}
			selected, err := PathsFromRegistry(*registry, *authority)
			if err != nil {
				fmt.Fprintf(os.Stderr, "extract: %v\n", err)
				os.Exit(1)
			}
			paths = selected
		}
		if *root == "" || len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "extract: --root and an explicit registry authority or at least one --path must be supplied together")
			os.Exit(1)
		}
		request, err := RequestFromPaths(*root, paths, *versionLiteral)
		if err != nil {
			fmt.Fprintf(os.Stderr, "extract: %v\n", err)
			os.Exit(1)
		}
		run(request)
		return
	}

	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
		os.Exit(1)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fmt.Fprintln(os.Stderr, "decode request: trailing JSON value")
		os.Exit(1)
	}
	run(request)
}

func run(request Request) {
	output, err := Extract(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
		os.Exit(1)
	}
}

type pathFlags []string

func (paths *pathFlags) String() string {
	return fmt.Sprint([]string(*paths))
}

func (paths *pathFlags) Set(path string) error {
	*paths = append(*paths, path)
	return nil
}
