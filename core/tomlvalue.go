package core

import "github.com/vektah/gqlparser/v2/ast"

// TOMLValue is a state carrier for a TOML value.
//
// The canonical value model is held in Data, encoded as JSON, which allows the
// typed accessors (asString, asInteger, field, ...) to reuse the same logic as
// JSONValue. Source, when set, holds the original TOML text and is used to
// preserve comments and formatting when editing a document.
type TOMLValue struct {
	// Data is the value model, encoded as JSON for uniform typed access.
	Data []byte

	// Source, when non-nil, is the original TOML text this value was decoded
	// from. It is used to preserve comments, key ordering and formatting when
	// editing. It is only meaningful for top-level table/document values.
	Source []byte
}

func (*TOMLValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TOMLValue",
		NonNull:   true,
	}
}
