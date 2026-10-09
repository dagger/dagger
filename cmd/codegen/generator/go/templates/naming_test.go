package templates

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
)

const identifierWordsObjectJSON = `
{
  "description": "A git commit",
  "fields": [
    {
      "args": [],
      "description": "The commit SHA.",
      "isDeprecated": false,
      "name": "sha",
      "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}
    },
    {
      "args": [],
      "description": "The parent commit SHAs.",
      "isDeprecated": false,
      "name": "parentShas",
      "type": {"kind": "NON_NULL", "ofType": {"kind": "LIST", "ofType": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}}}
    },
    {
      "args": [
        {
          "defaultValue": null,
          "description": "Number of characters.",
          "name": "length",
          "type": {"kind": "SCALAR", "name": "Int"}
        }
      ],
      "description": "The abbreviated SHA.",
      "isDeprecated": false,
      "name": "shortSha",
      "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}
    },
    {
      "args": [
        {
          "defaultValue": null,
          "description": "Where to push.",
          "name": "pushUrl",
          "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}
        }
      ],
      "description": "Set the push remote.",
      "isDeprecated": false,
      "name": "withRemote",
      "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "GitCommit"}}
    },
    {
      "args": [],
      "description": "The commit ID.",
      "isDeprecated": false,
      "name": "id",
      "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}
    }
  ],
  "kind": "OBJECT",
  "name": "GitCommit"
}
`

const identifierWordsEnumJSON = `
{
  "description": "Compression algorithm to use for image layers.",
  "enumValues": [
    {"description": "", "isDeprecated": false, "name": "Gzip"},
    {"description": "", "isDeprecated": false, "name": "EStarGZ"},
    {"description": "", "isDeprecated": false, "name": "TCP"}
  ],
  "kind": "ENUM",
  "name": "ImageLayerCompression"
}
`

// withIdentifierWords gives schema the words a v1.0.0 engine would put in
// its schema JSON.
func withIdentifierWords(schema *introspection.Schema) {
	ids := introspection.Identifiers{}
	ids.AddSchema(naming.Initial, schema)
	schema.Identifiers = ids
}

func TestFormatNamesFromIdentifierWords(t *testing.T) {
	schema, _ := loadSchemaFromTypeJSON(t, identifierWordsObjectJSON)

	// Without words: golint's initialisms, and schema names for parameters.
	require.Equal(t, "ParentShas", formatName("parentShas"))
	require.Equal(t, "pushUrl", formatArgName("pushUrl"))
	require.Equal(t, "Sha", formatName("sha"))

	withIdentifierWords(schema)
	require.Equal(t, "GitCommit", formatName("GitCommit"))
	require.Equal(t, "ParentSHAs", formatName("parentShas"))
	require.Equal(t, "ShortSHA", formatName("shortSha"))
	require.Equal(t, "SHA", formatName("sha"))
	require.Equal(t, "ID", formatName("id"))
	require.Equal(t, "pushURL", formatArgName("pushUrl"))
	// Names the schema has no words for keep the legacy conversion.
	require.Equal(t, "FooID", formatName("fooId"))
}

func TestObjectIdentifierWords(t *testing.T) {
	schema, object := loadSchemaFromTypeJSON(t, identifierWordsObjectJSON)
	withIdentifierWords(schema)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")

	got := renderTemplate(t, tmpl, object)

	// Go identifiers follow the words...
	require.Contains(t, got, "func (r *GitCommit) SHA(ctx context.Context) (string, error)")
	require.Contains(t, got, "func (r *GitCommit) ParentSHAs(ctx context.Context) ([]string, error)")
	require.Contains(t, got, "type GitCommitShortSHAOpts struct")
	require.Contains(t, got, "func (r *GitCommit) ShortSHA(ctx context.Context, opts ...GitCommitShortSHAOpts) (string, error)")
	require.Contains(t, got, "func (r *GitCommit) WithRemote(pushURL string) *GitCommit")
	require.Contains(t, got, `q = q.Arg("pushUrl", pushURL)`)
	// ...wire names don't.
	require.Contains(t, got, `q := r.query.Select("sha")`)
	require.Contains(t, got, `q := r.query.Select("parentShas")`)
	require.Contains(t, got, `q := r.query.Select("shortSha")`)
	require.Contains(t, got, `q = q.Arg("length", opts[i].Length)`)
	require.Contains(t, got, `return "GitCommit"`)

	// The old names stay as deprecated wrappers.
	require.Contains(t, got, "// Deprecated: use SHA instead.\nfunc (r *GitCommit) Sha(ctx context.Context) (string, error) {\n\treturn r.SHA(ctx)\n}")
	require.Contains(t, got, "// Deprecated: use ParentSHAs instead.\nfunc (r *GitCommit) ParentShas(ctx context.Context) ([]string, error) {\n\treturn r.ParentSHAs(ctx)\n}")
	require.Contains(t, got, "// Deprecated: use ShortSHA instead.\nfunc (r *GitCommit) ShortSha(ctx context.Context, opts ...GitCommitShortSHAOpts) (string, error) {\n\treturn r.ShortSHA(ctx, opts...)\n}")
	require.Contains(t, got, "// Deprecated: use GitCommitShortSHAOpts instead.\ntype GitCommitShortShaOpts = GitCommitShortSHAOpts")
	require.NotContains(t, got, "func (r *GitCommit) Id(")

	want := updateAndGetFixture(t, "testdata/object_identifier_words.golden", got)
	require.Equal(t, want, got)
}

// Without words, the same object renders exactly as before identifier words.
func TestObjectWithoutIdentifierWords(t *testing.T) {
	schema, object := loadSchemaFromTypeJSON(t, identifierWordsObjectJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")

	got := renderTemplate(t, tmpl, object)

	require.Contains(t, got, "func (r *GitCommit) Sha(ctx context.Context) (string, error)")
	require.Contains(t, got, "func (r *GitCommit) WithRemote(pushUrl string) *GitCommit")
	require.NotContains(t, got, "Deprecated")
}

func TestEnumIdentifierWords(t *testing.T) {
	schema, enum := loadSchemaFromTypeJSON(t, identifierWordsEnumJSON)
	withIdentifierWords(schema)
	tmpl := parseTemplateFiles(t, schema, "_types/enum.go.tmpl")

	got := renderTemplate(t, tmpl, enum)

	// Enum values uppercase initialisms like every Go identifier, and follow
	// word boundaries: EStarGZ is e-stargz, not estar-gz. The old strcase
	// names stay as deprecated aliases.
	require.Regexp(t, `ImageLayerCompressionEStarGZ\s+ImageLayerCompression = "EStarGZ"`, got)
	require.Regexp(t, `ImageLayerCompressionTCP\s+ImageLayerCompression = "TCP"`, got)
	require.Regexp(t, `ImageLayerCompressionGzip\s+ImageLayerCompression = "Gzip"`, got)
	require.Contains(t, got, "// Deprecated: use ImageLayerCompressionEStarGZ instead.\n\tImageLayerCompressionEstarGz ImageLayerCompression = ImageLayerCompressionEStarGZ")
	require.Contains(t, got, "// Deprecated: use ImageLayerCompressionTCP instead.\n\tImageLayerCompressionTcp ImageLayerCompression = ImageLayerCompressionTCP")
	require.NotContains(t, got, "use ImageLayerCompressionGzip instead.")
	// Wire names are unchanged.
	require.Contains(t, got, `case "EStarGZ":`)
	require.Contains(t, got, `return "EStarGZ"`)

	want := updateAndGetFixture(t, "testdata/enum_identifier_words.golden", got)
	require.Equal(t, want, got)
}
