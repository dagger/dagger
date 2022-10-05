package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"go.dagger.io/dagger/router"
)

// ErrNotImplementedYet is used to stub out API fields that aren't implemented
// yet.
var ErrNotImplementedYet = errors.New("not implemented yet")

func truncate(s string, lines *int) string {
	if lines == nil {
		return s
	}
	l := strings.SplitN(s, "\n", *lines+1)
	if *lines > len(l) {
		*lines = len(l)
	}
	return strings.Join(l[0:*lines], "\n")
}

// encodeID JSON marshals and base64-encodes an arbitrary payload.
func encodeID(payload any) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)

	return string(b64Bytes), nil
}

// decodeID base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func decodeID[T ~string](payload any, id T) error {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(id)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(id))
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v", payload, err)
	}

	jsonBytes = jsonBytes[:n]

	return json.Unmarshal(jsonBytes, payload)
}

// stringResolver is used to generate a scalar resolver for a stringable type.
func stringResolver[T ~string](sample T) router.ScalarResolver {
	return router.ScalarResolver{
		Serialize: func(value any) any {
			switch v := value.(type) {
			case string, T:
				return v
			default:
				panic(fmt.Sprintf("unexpected %T type %T", sample, v))
			}
		},
		ParseValue: func(value any) any {
			switch v := value.(type) {
			case string:
				return T(v)
			default:
				panic(fmt.Sprintf("unexpected %T value type %T: %+v", sample, v, v))
			}
		},
		ParseLiteral: func(valueAST ast.Value) any {
			switch valueAST := valueAST.(type) {
			case *ast.StringValue:
				return T(valueAST.Value)
			default:
				panic(fmt.Sprintf("unexpected %T literal type: %T", sample, valueAST))
			}
		},
	}
}

func defToState(def *pb.Definition) (llb.State, error) {
	if def.Def == nil {
		// NB(vito): llb.Scratch().Marshal().ToPB() produces an empty
		// *pb.Definition. If we don't convert it properly back to a llb.Scratch()
		// we'll hit 'cannot marshal empty definition op' when trying to marshal it
		// again.
		return llb.Scratch(), nil
	}

	defop, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}

	return llb.NewState(defop), nil
}

func absPath(workDir string, path_ string) string {
	if path.IsAbs(path_) {
		return path_
	}

	if workDir == "" {
		workDir = "/"
	}

	return path.Join(workDir, path_)
}
