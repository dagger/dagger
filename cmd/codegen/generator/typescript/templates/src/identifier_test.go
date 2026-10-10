package test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
	"github.com/dagger/dagger/cmd/codegen/internal/testnames"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// identifierSchemaJSON has names the engine recases: a field (shortSha), a
// required arg (callId), an optional arg (pushUrl), a field whose Opts type is
// recased (http), and an input object field (callId), which goes over the
// wire as is.
const identifierSchemaJSON = `
[
  {
    "kind": "OBJECT",
    "name": "Query",
    "fields": [
      {
        "name": "http",
        "args": [
          {"name": "url", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
          {"name": "pushUrl", "description": "Where to push.", "type": {"kind": "SCALAR", "name": "String"}}
        ],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "GitCommit"}}
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "OBJECT",
    "name": "GitCommit",
    "fields": [
      {
        "name": "shortSha",
        "args": [],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}
      },
      {
        "name": "withToolResult",
        "args": [
          {"name": "callId", "description": "The tool call.", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
          {"name": "block", "type": {"kind": "INPUT_OBJECT", "name": "ToolResultInput"}}
        ],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "GitCommit"}}
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "INPUT_OBJECT",
    "name": "ToolResultInput",
    "fields": null,
    "inputFields": [
      {"name": "callId", "type": {"kind": "SCALAR", "name": "String"}}
    ],
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "ENUM",
    "name": "NetworkProtocol",
    "fields": null,
    "inputFields": null,
    "interfaces": [],
    "enumValues": [
      {"name": "TCP"},
      {"name": "GITHUB_SSH"}
    ],
    "possibleTypes": null
  }
]
`

func identifierSchema(t *testing.T, engineNames bool) *introspection.Schema {
	t.Helper()
	schema := objectsInit(t, identifierSchemaJSON)
	if engineNames {
		testnames.Load(&schema, templates.NameFormats...)
	}
	generator.SetSchema(&schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	return &schema
}

func TestEngineFormattedNames(t *testing.T) {
	got := renderAPI(t, identifierSchema(t, true), "v1.0.0")

	// methods, with a deprecated alias under the old name
	require.Contains(t, got, "shortSHA = async (): Promise<string> => {")
	require.Contains(t, got, `"shortSha",`)
	require.Contains(t, got, `shortSha: GitCommit["shortSHA"] = (...args) =>`)

	// required args: the TS name is the parameter, the schema name the key
	require.Contains(t, got, "withToolResult = (callID: string,")
	require.Contains(t, got, "callId:callID,")
	require.Contains(t, got, "@param callID The tool call.")

	// optional args: opts keys are mapped back to the schema name, and the
	// old key is still accepted
	require.Contains(t, got, "export type ClientHTTPOpts = {")
	require.Contains(t, got, "pushURL?: string")
	require.Contains(t, got, "@deprecated use pushURL instead.")
	require.Contains(t, got, "pushUrl?: string")
	require.Contains(t, got, "@param opts.pushURL Where to push.")
	require.Contains(t, got, "...opts, pushURL: undefined, pushUrl: opts?.pushURL ?? opts?.pushUrl")
	require.Contains(t, got, "export type ClientHttpOpts = ClientHTTPOpts")
	require.Contains(t, got, `"http",`)

	// input object fields go over the wire as they are
	require.Contains(t, got, "export type ToolResultInput = {\n  callId?: string")

	// enum members: PASCAL / CAPITALIZED, values untouched
	require.Contains(t, got, `Tcp = "TCP",`)
	require.Contains(t, got, `GitHubSsh = "GITHUB_SSH",`)
	require.Contains(t, got, `return "GITHUB_SSH"`)

	// type names are left alone
	require.Contains(t, got, "export class GitCommit extends BaseClient")
	require.Contains(t, got, "export function NetworkProtocolValueToName(")
}

func TestEngineFormattedNamesFallback(t *testing.T) {
	got := renderAPI(t, identifierSchema(t, false), "v1.0.0")

	require.Contains(t, got, "shortSha = async (): Promise<string> => {")
	require.NotContains(t, got, "shortSHA")
	require.Contains(t, got, "withToolResult = (callId: string,")
	require.NotContains(t, got, "callID")
	require.Contains(t, got, "export type ClientHttpOpts = {")
	require.NotContains(t, got, "ClientHTTPOpts")
	require.NotContains(t, got, "pushURL")
	require.NotContains(t, got, "@deprecated")
	require.Contains(t, got, `GithubSsh = "GITHUB_SSH",`)
}
