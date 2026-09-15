package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// source is a decoded recipe plus where it came from.
type source struct {
	label string

	encoded  string // base64 as stored
	rawBytes int    // decoded protobuf byte count

	id    *call.ID
	dag   *callpbv1.DAG
	graph *graph
}

// load reads a base64 ID from path (stdin if empty or "-").
func load(path string) (*source, error) {
	var data []byte
	var err error
	label := path
	if path == "" || path == "-" {
		label = "<stdin>"
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}

	src := &source{label: label}

	str := strings.TrimSpace(string(data))
	src.encoded = str

	if raw, err := base64.StdEncoding.DecodeString(str); err == nil {
		src.rawBytes = len(raw)
	}

	src.id = new(call.ID)
	if err := src.id.Decode(str); err != nil {
		return nil, fmt.Errorf("%s: decode ID: %w", label, err)
	}

	// Round-trip back to the proto DAG: the flat callsByDigest map is the
	// honest data-oriented view, and it includes calls reachable only as
	// nested arguments (LiteralID), not just the receiver spine.
	src.dag, err = src.id.ToProto()
	if err != nil {
		return nil, fmt.Errorf("%s: to proto: %w", label, err)
	}
	recipe := src.dag.GetRecipe()
	if recipe == nil {
		return nil, fmt.Errorf("%s: not a recipe-form ID (handle-form IDs carry no call DAG)", label)
	}
	src.graph = newGraph(recipe)
	return src, nil
}
