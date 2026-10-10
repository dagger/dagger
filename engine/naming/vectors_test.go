package naming

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateVectors = flag.Bool("update", false, "regenerate testdata/vectors.json")

const vectorsPath = "testdata/vectors.json"

// sharedVectorExtras are inputs for the shared vectors beyond the Test
// Vectors table: the other non-error inputs of this package's tests, and
// names from the core schema.
var sharedVectorExtras = []string{
	// TestParseDetails.
	"IDsFoo", "HTTPSettings", "HTTPSServer", "GetAs", "MyAPIsList", "ios",
	"https", "3D", "123Foo", "foo_2_bar", "utf8_string", "runE2EAPITest",
	"Get4KStream", "HTTPAPIS", "MY_HTTPAPI_CLIENT", "UTF8JSON", "HTTPXAPI",
	"httpapi", "HTTPXAPIClient", "PostgreSQL", "gRPCAPI", "iOSSDK",
	"foo.bar-baz qux", "__init__",
	// TestKnownLimitations.
	"base64", "int32", "e2eapi",
	// TestAllAcronymNames.
	"htmlURL", "HTTPAPI", "GitHubAPI", "IPv6URL", "sha256URL", "IDs", "GPUsAPI",
	// The core schema.
	"JSONValue", "LLMTokenUsage", "LLMMessageRole", "SDKConfig", "HTTPState",
	"GitRepository", "CacheSharingMode", "SHARED", "ImageMediaTypes", "OCI",
	"withGPU", "withMCPServer", "asJSON", "traceURL", "commitSHA", "shortSha",
	"parentShas", "pushUrl", "asSdkName", "withoutUri", "vcsGeneratedPaths",
	"insecureSkipTLSVerify", "sshfsVolume",
}

// vectorWord is the JSON form of a word in testdata/vectors.json: kind, text
// and suffix as in the core API's IdentifierWord, plus its CAPITALIZED form.
type vectorWord struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Suffix string `json:"suffix"`
	// Capitalized is the word's CAPITALIZED-style form, without the suffix.
	Capitalized string `json:"capitalized"`
}

type vector struct {
	Input   string            `json:"input"`
	Words   []vectorWord      `json:"words"`
	Formats map[string]string `json:"formats"`
}

func vectorWords(id Identifier) []vectorWord {
	words := make([]vectorWord, len(id.Words))
	for i, w := range id.Words {
		bare := w
		bare.Suffix = ""
		words[i] = vectorWord{
			Kind:        w.Kind.String(),
			Text:        w.Text,
			Suffix:      w.Suffix,
			Capitalized: Identifier{Words: []Word{bare}}.Format(Pascal, Capitalized),
		}
	}
	return words
}

func sharedVectors(t *testing.T) []vector {
	seen := map[string]bool{}
	var inputs []string
	for _, tc := range testVectors {
		inputs = append(inputs, tc.input)
	}
	inputs = append(inputs, sharedVectorExtras...)

	var vectors []vector
	for _, input := range inputs {
		if seen[input] {
			continue
		}
		seen[input] = true
		id := mustParse(t, input)
		vectors = append(vectors, vector{
			Input: input,
			Words: vectorWords(id),
			Formats: map[string]string{
				"PASCAL":             id.Format(Pascal, Uppercase),
				"PASCAL_CAPITALIZED": id.Format(Pascal, Capitalized),
				"CAMEL":              id.Format(Camel, Uppercase),
				"CAMEL_CAPITALIZED":  id.Format(Camel, Capitalized),
				"SNAKE":              id.Format(Snake, Uppercase),
				"SCREAMING_SNAKE":    id.Format(ScreamingSnake, Uppercase),
				"KEBAB":              id.Format(Kebab, Uppercase),
				"FLAT":               id.Format(Flat, Uppercase),
			},
		})
	}
	return vectors
}

// TestSharedVectors keeps testdata/vectors.json, the shared formatting test
// vectors, in sync with the package. Regenerate it with:
//
//	go test ./engine/naming -run TestSharedVectors -update
func TestSharedVectors(t *testing.T) {
	data, err := json.MarshalIndent(sharedVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')

	if *updateVectors {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	existing, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update)", vectorsPath, err)
	}
	if !bytes.Equal(existing, data) {
		t.Errorf("%s is stale; regenerate with: go test ./engine/naming -run TestSharedVectors -update", vectorsPath)
	}
}

// formatVectorWords formats words with only the fields in the JSON, without a
// dictionary.
func formatVectorWords(words []vectorWord, casing string) string {
	lower := func(w vectorWord) string { return strings.ToLower(w.Text + w.Suffix) }
	capForm := func(w vectorWord, capitalized bool) string {
		if capitalized || w.Kind == "WORD" {
			return w.Capitalized + w.Suffix
		}
		return upperFirst(w.Text) + w.Suffix
	}
	var out string
	for i, w := range words {
		switch casing {
		case "PASCAL", "PASCAL_CAPITALIZED":
			out += capForm(w, casing == "PASCAL_CAPITALIZED")
		case "CAMEL", "CAMEL_CAPITALIZED":
			if i == 0 {
				out += lower(w)
			} else {
				out += capForm(w, casing == "CAMEL_CAPITALIZED")
			}
		case "SNAKE", "SCREAMING_SNAKE", "KEBAB":
			if i > 0 {
				if casing == "KEBAB" {
					out += "-"
				} else {
					out += "_"
				}
			}
			if casing == "SCREAMING_SNAKE" {
				out += strings.ToUpper(w.Text + w.Suffix)
			} else {
				out += lower(w)
			}
		case "FLAT":
			out += lower(w)
		}
	}
	return out
}

// The words in the JSON are enough to reproduce Format without a dictionary.
func TestSharedVectorsFormatFromWords(t *testing.T) {
	for _, v := range sharedVectors(t) {
		for casing, want := range v.Formats {
			if got := formatVectorWords(v.Words, casing); got != want {
				t.Errorf("%q in %s: words format as %q, want %q", v.Input, casing, got, want)
			}
		}
	}
}
