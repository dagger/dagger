package introspection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/naming"
)

// sharedVector is an entry of engine/naming/testdata/vectors.json.
type sharedVector struct {
	Input   string            `json:"input"`
	Words   []IdentifierWord  `json:"words"`
	Formats map[string]string `json:"formats"`
}

var vectorFormats = map[string]struct {
	casing naming.Casing
	style  naming.AcronymStyle
}{
	"PASCAL":             {naming.Pascal, naming.Uppercase},
	"PASCAL_CAPITALIZED": {naming.Pascal, naming.Capitalized},
	"CAMEL":              {naming.Camel, naming.Uppercase},
	"CAMEL_CAPITALIZED":  {naming.Camel, naming.Capitalized},
	"SNAKE":              {naming.Snake, naming.Uppercase},
	"SCREAMING_SNAKE":    {naming.ScreamingSnake, naming.Uppercase},
	"KEBAB":              {naming.Kebab, naming.Uppercase},
	"FLAT":               {naming.Flat, naming.Uppercase},
}

func loadSharedVectors(t *testing.T) []sharedVector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "engine", "naming", "testdata", "vectors.json"))
	require.NoError(t, err)
	var vectors []sharedVector
	require.NoError(t, json.Unmarshal(data, &vectors))
	require.NotEmpty(t, vectors)
	return vectors
}

// The words the engine writes are the shared vectors' words, and rebuilding
// a naming.Identifier from them formats exactly like the engine.
func TestIdentifierWordsMatchSharedVectors(t *testing.T) {
	for _, v := range loadSharedVectors(t) {
		id, err := naming.Parse(v.Input)
		require.NoError(t, err)
		require.Equal(t, v.Words, NewIdentifierWords(id), v.Input)

		schema := &Schema{Identifiers: Identifiers{v.Input: v.Words}}
		rebuilt, ok := schema.Identifier(v.Input)
		require.True(t, ok, v.Input)
		require.Equal(t, v.Input, rebuilt.Name)
		for name, f := range vectorFormats {
			want, ok := v.Formats[name]
			require.True(t, ok, "%s: missing format %s", v.Input, name)
			require.Equal(t, want, naming.Format(rebuilt, f.casing, f.style), "%s in %s", v.Input, name)
			require.Equal(t, id.Format(f.casing, f.style), naming.Format(rebuilt, f.casing, f.style), "%s in %s", v.Input, name)
		}
	}
}

func TestSchemaIdentifierMissing(t *testing.T) {
	_, ok := (&Schema{}).Identifier("httpClient")
	require.False(t, ok)

	schema := &Schema{Identifiers: Identifiers{
		"future": {{Kind: "SOMETHING_NEW", Text: "x", Capitalized: "X"}},
	}}
	_, ok = schema.Identifier("future")
	require.False(t, ok)
}

func TestIdentifiersAddSchema(t *testing.T) {
	schema := &Schema{
		Types: Types{
			{
				Kind: TypeKindObject,
				Name: "GitRepository",
				Fields: []*Field{{
					Name: "httpClient",
					Args: InputValues{{Name: "insecureSkipTLSVerify"}},
				}},
			},
			{
				Kind:        TypeKindInputObject,
				Name:        "LLMContentBlockInput",
				InputFields: []InputValue{{Name: "commitSHA"}},
			},
			{
				Kind:       TypeKindEnum,
				Name:       "CacheSharingMode",
				EnumValues: []EnumValue{{Name: "SHARED"}, {Name: "__hidden"}},
			},
			{
				Kind:   TypeKindObject,
				Name:   "__Type",
				Fields: []*Field{{Name: "ofType"}},
			},
		},
	}
	ids := Identifiers{}
	ids.AddSchema(naming.Initial, schema)
	ids.Add(naming.Initial, "_") // no words: skipped

	require.ElementsMatch(t, []string{
		"GitRepository", "httpClient", "insecureSkipTLSVerify",
		"LLMContentBlockInput", "commitSHA", "CacheSharingMode", "SHARED",
	}, keys(ids))
	require.Equal(t, []IdentifierWord{
		{Kind: "ACRONYM", Text: "HTTP", Capitalized: "Http"},
		{Kind: "WORD", Text: "client", Capitalized: "Client"},
	}, ids["httpClient"])
}

func keys(ids Identifiers) []string {
	var out []string
	for k := range ids {
		out = append(out, k)
	}
	return out
}

func TestResponseIdentifiersJSON(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		resp := Response{
			Schema: &Schema{Identifiers: Identifiers{
				"httpClient": {
					{Kind: "ACRONYM", Text: "HTTP", Capitalized: "Http"},
					{Kind: "WORD", Text: "client", Capitalized: "Client"},
				},
			}},
			SchemaVersion: "v1.0.0",
		}
		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &raw))
		require.Contains(t, raw, "__schema")
		require.Contains(t, raw, "__schemaVersion")
		require.JSONEq(t, `{"httpClient": [
			{"kind": "ACRONYM", "text": "HTTP", "suffix": "", "capitalized": "Http"},
			{"kind": "WORD", "text": "client", "suffix": "", "capitalized": "Client"}
		]}`, string(raw["__identifiers"]))

		var back Response
		require.NoError(t, json.Unmarshal(data, &back))
		require.Equal(t, "v1.0.0", back.SchemaVersion)
		require.Equal(t, resp.Schema.Identifiers, back.Schema.Identifiers)
		id, ok := back.Schema.Identifier("httpClient")
		require.True(t, ok)
		require.Equal(t, "HttpClient", id.Format(naming.Pascal, naming.Capitalized))

		// Through a pointer, too.
		ptrData, err := json.Marshal(&resp)
		require.NoError(t, err)
		require.JSONEq(t, string(data), string(ptrData))
	})

	t.Run("absent", func(t *testing.T) {
		data, err := json.Marshal(Response{Schema: &Schema{}, SchemaVersion: "v0.19.0"})
		require.NoError(t, err)
		require.NotContains(t, string(data), "__identifiers")

		var back Response
		require.NoError(t, json.Unmarshal(data, &back))
		require.Nil(t, back.Schema.Identifiers)
	})
}
