package naming

import (
	"errors"
	"strings"
	"testing"
)

// testVectors is the Test Vectors table of hack/designs/identifier-casing.md.
// Words use its notation: ^ marks ACRONYM, * marks TERM, + a suffix.
var testVectors = []struct {
	input, words, pascal, camel, snake, pascalCapitalized string
}{
	{"HTTPClient", "^HTTP · client", "HTTPClient", "httpClient", "http_client", "HttpClient"},
	{"httpClient", "^HTTP · client", "HTTPClient", "httpClient", "http_client", "HttpClient"},
	{"HttpClient", "^HTTP · client", "HTTPClient", "httpClient", "http_client", "HttpClient"},
	{"http_client", "^HTTP · client", "HTTPClient", "httpClient", "http_client", "HttpClient"},
	{"HTTP_CLIENT", "^HTTP · client", "HTTPClient", "httpClient", "http_client", "HttpClient"},
	{"HTTPAPIClient", "^HTTP · ^API · client", "HTTPAPIClient", "httpAPIClient", "http_api_client", "HttpApiClient"},
	{"LLMContentBlock", "^LLM · content · block", "LLMContentBlock", "llmContentBlock", "llm_content_block", "LlmContentBlock"},
	{"E2ETest", "^E2E · test", "E2ETest", "e2eTest", "e2e_test", "E2eTest"},
	{"e2e_test", "^E2E · test", "E2ETest", "e2eTest", "e2e_test", "E2eTest"},
	{"prerequisiteSHAs", "prerequisite · ^SHA+s", "PrerequisiteSHAs", "prerequisiteSHAs", "prerequisite_shas", "PrerequisiteShas"},
	{"experimentalWithAllGPUs", "experimental · with · all · ^GPU+s", "ExperimentalWithAllGPUs", "experimentalWithAllGPUs", "experimental_with_all_gpus", "ExperimentalWithAllGpus"},
	{"userIds", "user · ^ID+s", "UserIDs", "userIDs", "user_ids", "UserIds"},
	{"listPRs", "list · ^PR+s", "ListPRs", "listPRs", "list_prs", "ListPrs"},
	{"IPv6Address", "*IPv6 · address", "IPv6Address", "ipv6Address", "ipv6_address", "Ipv6Address"},
	{"ipv6_address", "*IPv6 · address", "IPv6Address", "ipv6Address", "ipv6_address", "Ipv6Address"},
	{"OAuth2Token", "*OAuth+2 · token", "OAuth2Token", "oauth2Token", "oauth2_token", "Oauth2Token"},
	{"oauth2_token", "*OAuth+2 · token", "OAuth2Token", "oauth2Token", "oauth2_token", "Oauth2Token"},
	{"GitHubRepo", "*GitHub · repo", "GitHubRepo", "githubRepo", "github_repo", "GitHubRepo"},
	{"github_repo", "*GitHub · repo", "GitHubRepo", "githubRepo", "github_repo", "GitHubRepo"},
	{"iOSApp", "*iOS · app", "IOSApp", "iosApp", "ios_app", "IosApp"},
	{"macOSBuild", "*macOS · build", "MacOSBuild", "macosBuild", "macos_build", "MacOSBuild"},
	{"gRPCServer", "*gRPC · server", "GRPCServer", "grpcServer", "grpc_server", "GrpcServer"},
	{"Get3DModel", "get · ^3D · model", "Get3DModel", "get3DModel", "get_3d_model", "Get3DModel"},
	{"get3d_model", "get · ^3D · model", "Get3DModel", "get3DModel", "get_3d_model", "Get3DModel"},
	{"Base64URLEncode", "base64 · ^URL · encode", "Base64URLEncode", "base64URLEncode", "base64_url_encode", "Base64UrlEncode"},
	{"Win32API", "win32 · ^API", "Win32API", "win32API", "win32_api", "Win32Api"},
	{"HTTP2Server", "^HTTP+2 · server", "HTTP2Server", "http2Server", "http2_server", "Http2Server"},
	{"Sha256Sum", "^SHA+256 · sum", "SHA256Sum", "sha256Sum", "sha256_sum", "Sha256Sum"},
	{"md5sum", "^MD5 · sum", "MD5Sum", "md5Sum", "md5_sum", "Md5Sum"},
	{"S3Bucket", "s3 · bucket", "S3Bucket", "s3Bucket", "s3_bucket", "S3Bucket"},
	{"V1Beta1Foo", "v1 · beta1 · foo", "V1Beta1Foo", "v1Beta1Foo", "v1_beta1_foo", "V1Beta1Foo"},
	{"ModuleAOverlay", "module · a · overlay", "ModuleAOverlay", "moduleAOverlay", "module_a_overlay", "ModuleAOverlay"},
	{"IOStream", "^IO · stream", "IOStream", "ioStream", "io_stream", "IoStream"},
	{"Capital", "capital", "Capital", "capital", "capital", "Capital"},
	{"Identity", "identity", "Identity", "identity", "identity", "Identity"},
	{"HTTPXClient", "^HTTPX · client", "HTTPXClient", "httpxClient", "httpx_client", "HttpxClient"},
	{"httpx_client", "httpx · client", "HttpxClient", "httpxClient", "httpx_client", "HttpxClient"},
	{"E2EAPI", "^E2E · ^API", "E2EAPI", "e2eAPI", "e2e_api", "E2eApi"},
	{"JSONAPI", "^JSON · ^API", "JSONAPI", "jsonAPI", "json_api", "JsonApi"},
	{"HTMLURL", "^HTML · ^URL", "HTMLURL", "htmlURL", "html_url", "HtmlUrl"},
	{"HTTPX_CLIENT", "httpx · client", "HttpxClient", "httpxClient", "httpx_client", "HttpxClient"},
}

// unknownAcronyms are test vector inputs with acronyms the dictionary doesn't
// know, which the round-trip guarantee doesn't cover.
var unknownAcronyms = map[string]bool{
	"HTTPXClient": true,
}

// describe renders words in the test vector notation.
func describe(id Identifier) string {
	parts := make([]string, len(id.Words))
	for i, w := range id.Words {
		var b strings.Builder
		switch w.Kind {
		case KindAcronym:
			b.WriteByte('^')
		case KindTerm:
			b.WriteByte('*')
		}
		b.WriteString(w.Text)
		if w.Suffix != "" {
			b.WriteByte('+')
			b.WriteString(w.Suffix)
		}
		parts[i] = b.String()
	}
	return strings.Join(parts, " · ")
}

func mustParse(t *testing.T, name string) Identifier {
	t.Helper()
	id, err := Parse(name)
	if err != nil {
		t.Fatalf("Parse(%q): %v", name, err)
	}
	return id
}

func TestVectors(t *testing.T) {
	for _, tc := range testVectors {
		t.Run(tc.input, func(t *testing.T) {
			id := mustParse(t, tc.input)
			if got := describe(id); got != tc.words {
				t.Errorf("words: got %q, want %q", got, tc.words)
			}
			for _, f := range []struct {
				casing Casing
				style  AcronymStyle
				want   string
			}{
				{Pascal, Uppercase, tc.pascal},
				{Camel, Uppercase, tc.camel},
				{Snake, Uppercase, tc.snake},
				{Pascal, Capitalized, tc.pascalCapitalized},
			} {
				if got := id.Format(f.casing, f.style); got != f.want {
					t.Errorf("%s/%s: got %q, want %q", f.casing, f.style, got, f.want)
				}
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		input string
		err   error
	}{
		{"Café", ErrNonASCII},
		{"", ErrNoWords},
		{"_", ErrNoWords},
		{"--.", ErrNoWords},
	} {
		if _, err := Parse(tc.input); !errors.Is(err, tc.err) {
			t.Errorf("Parse(%q): got error %v, want %v", tc.input, err, tc.err)
		}
	}
}

func TestFormatCasings(t *testing.T) {
	id := mustParse(t, "HTTPAPIClient")
	gh := mustParse(t, "GitHubRepo")
	for _, tc := range []struct {
		casing    Casing
		http, git string
	}{
		{Pascal, "HTTPAPIClient", "GitHubRepo"},
		{Camel, "httpAPIClient", "githubRepo"},
		{Snake, "http_api_client", "github_repo"},
		{ScreamingSnake, "HTTP_API_CLIENT", "GITHUB_REPO"},
		{Kebab, "http-api-client", "github-repo"},
		{Flat, "httpapiclient", "githubrepo"},
	} {
		if got := id.Format(tc.casing, Uppercase); got != tc.http {
			t.Errorf("%s: got %q, want %q", tc.casing, got, tc.http)
		}
		if got := gh.Format(tc.casing, Uppercase); got != tc.git {
			t.Errorf("%s: got %q, want %q", tc.casing, got, tc.git)
		}
	}
	// The acronym style only affects casings that capitalize words.
	for _, c := range []Casing{Snake, ScreamingSnake, Kebab, Flat} {
		if a, b := id.Format(c, Uppercase), id.Format(c, Capitalized); a != b {
			t.Errorf("%s: style changed output: %q vs %q", c, a, b)
		}
	}
}

func TestParseDetails(t *testing.T) {
	for _, tc := range []struct{ input, words string }{
		// Pieces and the plural exception.
		{"IDsFoo", "^ID+s · foo"},
		{"HTTPSettings", "^HTTP · settings"},
		{"HTTPSServer", "^HTTPS · server"},
		{"GetAs", "get · as"},
		{"MyAPIsList", "my · ^API+s · list"},
		// Plural matches lose to exact ones.
		{"ios", "*iOS"},
		{"https", "^HTTPS"},
		// Digit gluing and digit-led names.
		{"3D", "^3D"},
		{"123Foo", "123 · foo"},
		{"foo_2_bar", "foo2 · bar"},
		{"utf8_string", "^UTF8 · string"},
		{"runE2EAPITest", "run · ^E2E · ^API · test"},
		{"Get4KStream", "get4 · k · stream"},
		// All-caps chunks split only where entries cover them completely.
		{"HTTPAPIS", "^HTTP · ^API+s"},
		{"MY_HTTPAPI_CLIENT", "my · ^HTTP · ^API · client"},
		{"UTF8JSON", "^UTF8 · ^JSON"},
		{"HTTPXAPI", "httpxapi"},
		{"httpapi", "httpapi"},
		// Known limitations.
		{"HTTPXAPIClient", "^HTTPXAPI · client"},
		{"PostgreSQL", "postgre · ^SQL"},
		{"gRPCAPI", "g · ^RPC · ^API"},
		{"iOSSDK", "i · ^OSSDK"},
		// Separators are hard boundaries.
		{"foo.bar-baz qux", "foo · bar · baz · qux"},
		{"__init__", "init"},
	} {
		if got := describe(mustParse(t, tc.input)); got != tc.words {
			t.Errorf("Parse(%q): got %q, want %q", tc.input, got, tc.words)
		}
	}
}

func TestKnownLimitations(t *testing.T) {
	for _, tc := range []struct {
		input  string
		casing Casing
		want   string
	}{
		{"httpx_client", Pascal, "HttpxClient"},
		{"PostgreSQL", Snake, "postgre_sql"},
		{"Sha256Sum", Pascal, "SHA256Sum"},
		{"HttpClient", Pascal, "HTTPClient"},
		{"md5sum", Snake, "md5_sum"},
		{"base64", Snake, "base64"},
		{"int32", Snake, "int32"},
		{"Get4KStream", Snake, "get4_k_stream"},
		{"E2ETest", Kebab, "e2e-test"},
		// Lowercase input never splits; all-caps input splits only where
		// dictionary entries cover a whole chunk.
		{"e2eapi", Pascal, "E2eapi"},
		{"HTTPXAPI", Snake, "httpxapi"},
		{"HTTP_CLIENT", Pascal, "HTTPClient"},
	} {
		got, err := Convert(tc.input, tc.casing, Uppercase)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s(%q): got %q, want %q", tc.casing, tc.input, got, tc.want)
		}
	}
}

// HTTPXClient isn't in the dictionary, so its acronym only survives formats
// that keep capitals.
func TestUnknownAcronymLostThroughLowercase(t *testing.T) {
	id := mustParse(t, "HTTPXClient")
	for _, c := range []Casing{Snake, ScreamingSnake, Kebab} {
		for _, s := range AcronymStyles {
			again := mustParse(t, id.Format(c, s))
			if got := describe(again); got != "httpx · client" {
				t.Errorf("%s/%s: got %q, want %q", c, s, got, "httpx · client")
			}
		}
	}
	for _, f := range []struct {
		casing Casing
		style  AcronymStyle
	}{{Pascal, Uppercase}, {Camel, Uppercase}} {
		again := mustParse(t, id.Format(f.casing, f.style))
		want := "^HTTPX · client"
		if f.casing == Camel {
			// CAMEL lowercases the first word.
			want = "httpx · client"
		}
		if got := describe(again); got != want {
			t.Errorf("%s/%s: got %q, want %q", f.casing, f.style, got, want)
		}
	}
}

func TestDictionary(t *testing.T) {
	for _, tc := range []struct {
		spelling, capitalized string
		kind                  WordKind
	}{
		{"HTTP", "Http", KindAcronym},
		{"E2E", "E2e", KindAcronym},
		{"3D", "3D", KindAcronym},
		{"IPv6", "Ipv6", KindTerm},
		{"gRPC", "Grpc", KindTerm},
		{"GitHub", "GitHub", KindTerm},
		{"iOS", "Ios", KindTerm},
	} {
		term, ok := Initial.lookup(strings.ToLower(tc.spelling))
		if !ok {
			t.Errorf("%s: not in the dictionary", tc.spelling)
			continue
		}
		if term.Spelling != tc.spelling || term.Capitalized != tc.capitalized || term.Kind() != tc.kind {
			t.Errorf("%s: got %+v (%s), want %s/%s (%s)", tc.spelling, *term, term.Kind(), tc.spelling, tc.capitalized, tc.kind)
		}
	}

	for _, terms := range [][]Term{
		{{Spelling: "HTTP"}, {Spelling: "http"}},
		{{Spelling: "GitHub"}},
		{{Spelling: "4K"}},
		{{Spelling: "123", Capitalized: "123"}},
		{{Spelling: "A-B"}},
		{{Spelling: "IPv6", Capitalized: "Ipv4"}},
	} {
		if _, err := NewDictionary(terms); err == nil {
			t.Errorf("NewDictionary(%+v): expected an error", terms)
		}
	}
}

// guaranteeInputs are the names the guarantee tests check: the test vector
// inputs and every casing of them except FLAT, which is output-only.
func guaranteeInputs(t *testing.T) []string {
	var names []string
	for _, tc := range testVectors {
		names = append(names, tc.input)
		id := mustParse(t, tc.input)
		for _, c := range Casings {
			if c == Flat {
				continue
			}
			for _, s := range AcronymStyles {
				names = append(names, id.Format(c, s))
			}
		}
	}
	return names
}

// Guarantee 1: names the dictionary covers keep their words through every
// casing except FLAT, in both acronym styles.
func checkRoundTrip(t *testing.T, name string) {
	t.Helper()
	id := mustParse(t, name)
	want := describe(id)
	for _, c := range Casings {
		if c == Flat {
			continue
		}
		for _, s := range AcronymStyles {
			formatted := id.Format(c, s)
			if got := describe(mustParse(t, formatted)); got != want {
				t.Errorf("%q via %s/%s %q: got words %q, want %q", name, c, s, formatted, got, want)
			}
		}
	}
}

// Guarantee 2: formatting a formatted name again changes nothing. With
// guarantee 3 (canonical names come back unchanged) this is the same check:
// a formatted name is canonical for its casing.
func checkIdempotent(t *testing.T, name string) {
	t.Helper()
	id := mustParse(t, name)
	for _, c := range []Casing{Pascal, Camel, Snake, ScreamingSnake} {
		once := id.Format(c, Uppercase)
		if twice := mustParse(t, once).Format(c, Uppercase); twice != once {
			t.Errorf("%q in %s: %q formats again as %q", name, c, once, twice)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, tc := range testVectors {
		if unknownAcronyms[tc.input] {
			continue
		}
		checkRoundTrip(t, tc.input)
	}
}

func TestIdempotent(t *testing.T) {
	for _, name := range guaranteeInputs(t) {
		checkIdempotent(t, name)
	}
}

// Names made only of acronyms and terms are all caps in PASCAL/UPPERCASE, so
// they rely on all-caps input splitting by dictionary cover.
func TestAllAcronymNames(t *testing.T) {
	for _, name := range []string{"htmlURL", "JSONAPI", "HTTPAPI", "GitHubAPI", "IPv6URL", "E2EAPI", "sha256URL", "IDs", "GPUsAPI"} {
		checkRoundTrip(t, name)
		checkIdempotent(t, name)
		id := mustParse(t, name)
		if pascal := id.Format(Pascal, Uppercase); mustParse(t, pascal).Format(Pascal, Uppercase) != pascal {
			t.Errorf("canonical PASCAL name %q came back changed", pascal)
		}
	}
}

func TestCanonicalUnchanged(t *testing.T) {
	for _, tc := range testVectors {
		for _, f := range []struct {
			casing Casing
			name   string
		}{
			{Pascal, tc.pascal},
			{Camel, tc.camel},
			{Snake, tc.snake},
		} {
			if got := mustParse(t, f.name).Format(f.casing, Uppercase); got != f.name {
				t.Errorf("canonical %s name %q came back as %q", f.casing, f.name, got)
			}
		}
	}
}
