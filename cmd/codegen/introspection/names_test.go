package introspection

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNameFormats(t *testing.T) {
	formats, err := ParseNameFormats("SNAKE:UPPERCASE, PASCAL:CAPITALIZED,")
	require.NoError(t, err)
	require.Equal(t, []NameFormat{
		{Casing: CasingSnake, Acronyms: AcronymsUppercase},
		{Casing: CasingPascal, Acronyms: AcronymsCapitalized},
	}, formats)
	require.Equal(t, "SNAKE:UPPERCASE", formats[0].String())

	formats, err = ParseNameFormats("")
	require.NoError(t, err)
	require.Empty(t, formats)

	for _, bad := range []string{"SNAKE", "SNAKE:", ":UPPERCASE", "snake:uppercase", "SNAKE:UPPER:CASE"} {
		_, err := ParseNameFormat(bad)
		require.Error(t, err, bad)
	}
}

func namesTestSchema(withFormatIdentifiers bool) *Schema {
	queryFields := []*Field{{Name: "httpClient", Args: InputValues{{Name: "insecureSkipTLSVerify"}}}}
	if withFormatIdentifiers {
		queryFields = append(queryFields, &Field{Name: "formatIdentifiers", Args: InputValues{{Name: "names"}}})
	}
	schema := &Schema{
		Types: Types{
			{Kind: TypeKindObject, Name: "Query", Fields: queryFields},
			{
				Kind:        TypeKindInputObject,
				Name:        "LLMContentBlockInput",
				InputFields: []InputValue{{Name: "commitSHA"}, {Name: "_"}},
			},
			{
				Kind:       TypeKindEnum,
				Name:       "CacheSharingMode",
				EnumValues: []EnumValue{{Name: "SHARED"}, {Name: "__hidden"}},
			},
			{Kind: TypeKindObject, Name: "__Type", Fields: []*Field{{Name: "ofType"}}},
		},
	}
	schema.QueryType.Name = "Query"
	return schema
}

func TestSchemaNames(t *testing.T) {
	require.Equal(t, []string{
		"CacheSharingMode", "LLMContentBlockInput", "Query", "SHARED",
		"commitSHA", "formatIdentifiers", "httpClient", "insecureSkipTLSVerify", "names",
	}, namesTestSchema(true).Names())
}

func TestHasFormatIdentifiers(t *testing.T) {
	require.True(t, namesTestSchema(true).HasFormatIdentifiers())
	require.False(t, namesTestSchema(false).HasFormatIdentifiers())
	require.False(t, (&Schema{}).HasFormatIdentifiers())
	require.False(t, (*Schema)(nil).HasFormatIdentifiers())
}

// Without Query.formatIdentifiers, nothing is formatted and no engine is
// needed: codegen keeps its legacy converters.
func TestFormatNamesGate(t *testing.T) {
	f := NameFormat{Casing: CasingSnake, Acronyms: AcronymsUppercase}

	schema := namesTestSchema(false)
	formatted, ok, err := FormatNames(t.Context(), nil, schema, []string{"httpClient"}, f.Casing, f.Acronyms)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, formatted)
	require.NoError(t, schema.LoadFormattedNames(t.Context(), nil, f))
	require.Nil(t, schema.FormattedNames)
	_, ok = schema.FormattedName("httpClient", f)
	require.False(t, ok)

	// With it, formatting needs an engine connection.
	schema = namesTestSchema(true)
	_, _, err = FormatNames(t.Context(), nil, schema, []string{"httpClient"}, f.Casing, f.Acronyms)
	require.Error(t, err)
	require.Error(t, schema.LoadFormattedNames(t.Context(), nil, f))
}

func TestFormattedName(t *testing.T) {
	snake := NameFormat{Casing: CasingSnake, Acronyms: AcronymsUppercase}
	pascal := NameFormat{Casing: CasingPascal, Acronyms: AcronymsUppercase}
	schema := namesTestSchema(true)
	schema.FormattedNames = map[NameFormat]map[string]string{
		snake: {"httpClient": "http_client"},
	}

	name, ok := schema.FormattedName("httpClient", snake)
	require.True(t, ok)
	require.Equal(t, "http_client", name)
	_, ok = schema.FormattedName("httpClient", pascal)
	require.False(t, ok)
	_, ok = schema.FormattedName("other", snake)
	require.False(t, ok)
	_, ok = (*Schema)(nil).FormattedName("httpClient", snake)
	require.False(t, ok)

	// Loaded formats are skipped, so no engine is needed for them.
	require.NoError(t, schema.LoadFormattedNames(t.Context(), nil, snake))

	// Filtered schemas keep the formatted names.
	name, ok = schema.Exclude("someModule").FormattedName("httpClient", snake)
	require.True(t, ok)
	require.Equal(t, "http_client", name)
}

func TestNamesFile(t *testing.T) {
	schema := namesTestSchema(false)
	data, err := json.Marshal(schema.NamesFile())
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))

	schema.FormattedNames = map[NameFormat]map[string]string{
		{Casing: CasingSnake, Acronyms: AcronymsUppercase}:     {"httpClient": "http_client"},
		{Casing: CasingPascal, Acronyms: AcronymsCapitalized}: {"httpClient": "HttpClient"},
	}
	data, err = json.Marshal(schema.NamesFile())
	require.NoError(t, err)
	require.JSONEq(t, `{
		"SNAKE:UPPERCASE": {"httpClient": "http_client"},
		"PASCAL:CAPITALIZED": {"httpClient": "HttpClient"}
	}`, string(data))
}
