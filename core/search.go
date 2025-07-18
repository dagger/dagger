package core

import "github.com/vektah/gqlparser/v2/ast"

type SearchResult struct {
	FilePath    string `field:"true"`
	LineNumber  int    `field:"true"`
	MatchedText string `field:"true"`
}

func (*SearchResult) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchResult",
		NonNull:   true,
	}
}

type rgJSON struct {
	Type string `json:"type"`
	Data struct {
		Path       rgContent `json:"path"`
		Lines      rgContent `json:"lines"`
		LineNumber int       `json:"line_number"`
		// unused... for now?
		// AbsoluteOffset int       `json:"absolute_offset"`
		// Submatches     []struct {
		// 	Match rgContent `json:"match"`
		// 	Start int       `json:"start"`
		// 	End   int       `json:"end"`
		// } `json:"submatches"`
	} `json:"data"`
}

type rgContent struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}
