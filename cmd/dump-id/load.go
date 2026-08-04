package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// sessionMeta mirrors the subset of internal/cmd/dagger.sessionMetadata that
// matters here: the saved conversation's recipe-form LLM ID plus provenance.
type sessionMeta struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	LLMID     string `json:"llm_id"`
	Branch    string `json:"branch,omitempty"`
}

// source is a decoded recipe plus where it came from.
type source struct {
	label string
	meta  *sessionMeta

	encoded  string // base64 as stored
	rawBytes int    // decoded protobuf byte count

	id    *call.ID
	dag   *callpbv1.DAG
	graph *graph
}

// load reads a base64 ID from path (stdin if empty or "-"). If the content
// looks like JSON it is treated as a saved session file and the llm_id field
// is used.
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
	if strings.HasPrefix(str, "{") {
		var meta sessionMeta
		if err := json.Unmarshal([]byte(str), &meta); err != nil {
			return nil, fmt.Errorf("%s: parse session JSON: %w", label, err)
		}
		if meta.LLMID == "" {
			return nil, fmt.Errorf("%s: session JSON has no llm_id", label)
		}
		src.meta = &meta
		str = strings.TrimSpace(meta.LLMID)
	}
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
