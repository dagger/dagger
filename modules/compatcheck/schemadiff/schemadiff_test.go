package schemadiff

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

//go:embed testdata/baseSchemaA.json
var baseA string

// with custom schema
//
//go:embed testdata/fullSchemaA.json
var fullA string

//go:embed testdata/baseSchemaB.json
var baseB string

// with custom schema
//
//go:embed testdata/fullSchemaB.json
var fullB string

func TestJsonDiff(t *testing.T) {
	fmt.Println(fullA)
	diff, err := Do(baseA, baseB, fullA, fullB)
	require.Nil(t, err)

	expectedDiff := `@ ["custom","twelve"]
- [12,13,14,15]
@ ["custom","Twelve"]
+ [12,13,14,15]
`

	require.Equal(t, expectedDiff, diff)
}
