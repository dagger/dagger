package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

func queryVertex(recorder *progrock.Recorder, fieldName string, parent, args any) (*progrock.VertexRecorder, error) {
	dig, err := queryDigest(fieldName, parent, args)
	if err != nil {
		return nil, fmt.Errorf("failed to compute query digest: %w", err)
	}

	var inputs []digest.Digest

	// Ensure we use any custom serialization defined on the args type when displaying this.
	// E.g. secret plaintext fields have a custom serialization that scrubs the value.
	argBytes, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}
	argMap := map[string]any{}
	if err := json.Unmarshal(argBytes, &argMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %w", err)
	}

	name := fieldName
	argStrs := []string{}
	for argName, val := range argMap {
		argName = strcase.ToLowerCamel(argName)
		// skip if val is zero value for its type
		if val == nil || reflect.DeepEqual(val, reflect.Zero(reflect.TypeOf(val)).Interface()) {
			continue
		}

		if dg, ok := val.(core.Digestible); ok {
			d, err := dg.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to compute digest for param %q: %w", argName, err)
			}

			inputs = append(inputs, d)

			// display digest instead
			val = d
		}

		jv, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal arg %s: %w", argName, err)
		}

		argStrs = append(argStrs, fmt.Sprintf("%s: %s", argName, jv))
	}
	if len(argStrs) > 0 {
		name += "(" + strings.Join(argStrs, ", ") + ")"
	}

	if edible, ok := parent.(core.Digestible); ok {
		id, err := edible.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to compute digest: %w", err)
		}

		inputs = append(inputs, id)
	}

	return recorder.Vertex(
		dig,
		name,
		progrock.WithInputs(inputs...),
		progrock.Internal(),
	), nil
}

func queryDigest(fieldName string, parent, args any) (digest.Digest, error) {
	type subset struct {
		Source any
		Field  string
		Args   any
	}

	payload, err := json.Marshal(subset{
		Source: parent,
		Field:  fieldName,
		Args:   args,
	})
	if err != nil {
		return "", err
	}

	return digest.SHA256.FromBytes(payload), nil
}
