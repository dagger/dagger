package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtractUsesLiteralAndPreservesStates(t *testing.T) {
	t.Parallel()
	request := Request{
		FormatVersion:      1,
		VersionLiteralName: "goSDKLibVersion",
		Files: []SourceFile{
			{
				Path: "core/sdk/go_sdk.go",
				Content: "package sdk\n\nconst goSDKLibVersion = \"actual-commit\" // v9.9.9\n" +
					"type Client[T any] struct{}\n" +
					"// Deprecated: use Client.\ntype Old struct{}\n",
			},
			{
				Path: "core/integration/module_test.go",
				Content: "package integration\nimport \"testing\"\n" +
					"func TestModule(t *testing.T) {\n" +
					"  t.Run(\"active\", func(t *testing.T) {})\n" +
					"  t.Run(\"skipped\", func(t *testing.T) { t.Skip(\"later\") })\n" +
					"  for _, tc := range []string{\"a\"} { t.Run(tc, func(t *testing.T) {}) }\n" +
					"}\n",
			},
		},
	}
	output, err := Extract(request)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if output.GoSDKLibVersion != "actual-commit" {
		t.Fatalf("version = %q, want evaluated literal", output.GoSDKLibVersion)
	}
	var states []string
	for _, item := range output.Items {
		states = append(states, item.Kind+":"+item.Name+":"+item.State)
		if !strings.HasPrefix(item.Fingerprint, "sha256:") {
			t.Fatalf("fingerprint = %q", item.Fingerprint)
		}
	}
	joined := strings.Join(states, "\n")
	for _, expected := range []string{
		"type:Client:active",
		"type:Old:deprecated",
		"subtest:active:active",
		"subtest:skipped:skipped",
		"dynamic-subtest:<dynamic>:active",
		"test-table:tc:active",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("items do not contain %q:\n%s", expected, joined)
		}
	}
}

func TestExtractIsIndependentOfFileOrder(t *testing.T) {
	t.Parallel()
	files := []SourceFile{
		{Path: "version.go", Content: "package example\nconst selected = \"abc\"\n"},
		{Path: "api.go", Content: "package example\nfunc Public[T any](value T) T { return value }\n"},
	}
	left, err := Extract(Request{FormatVersion: 1, Files: files, VersionLiteralName: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	files[0], files[1] = files[1], files[0]
	right, err := Extract(Request{FormatVersion: 1, Files: files, VersionLiteralName: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(left.Items) != len(right.Items) {
		t.Fatalf("item counts differ: %d and %d", len(left.Items), len(right.Items))
	}
	for index := range left.Items {
		if left.Items[index] != right.Items[index] {
			t.Fatalf("item %d differs: %#v and %#v", index, left.Items[index], right.Items[index])
		}
	}
}

func TestExtractAuthorityWithoutEngineVersionLiteral(t *testing.T) {
	t.Parallel()
	output, err := Extract(Request{
		FormatVersion: 1,
		Files: []SourceFile{{
			Path:    "client.go",
			Content: "package client\nfunc Connect() {}\n",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.GoSDKLibVersion != "" {
		t.Fatalf("unexpected engine version literal %q", output.GoSDKLibVersion)
	}
	if len(output.Items) != 1 || output.Items[0].Name != "Connect" {
		t.Fatalf("items = %#v", output.Items)
	}
}

func TestExtractPinnedRepositorySources(t *testing.T) {
	t.Parallel()
	engineSource := repositoryFile(t, "core/sdk/go_sdk.go")
	engine, err := Extract(Request{
		FormatVersion:      1,
		VersionLiteralName: "goSDKLibVersion",
		Files: []SourceFile{{
			Path:    "core/sdk/go_sdk.go",
			Content: engineSource,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.GoSDKLibVersion != "1309520660f6a5b35ef97b4fbe151e32a06a8dc5" {
		t.Fatalf("goSDKLibVersion = %q", engine.GoSDKLibVersion)
	}

	output, err := Extract(Request{
		FormatVersion: 1,
		Files: []SourceFile{
			{
				Path:    "cmd/codegen/introspection/introspection.go",
				Content: repositoryFile(t, "cmd/codegen/introspection/introspection.go"),
			},
			{
				Path:    "core/integration/module_engine_version_test.go",
				Content: repositoryFile(t, "core/integration/module_engine_version_test.go"),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var test, subtest bool
	for _, item := range output.Items {
		test = test || (item.Kind == "test" && item.Name == "TestModuleSchemaVersion")
		subtest = subtest || (item.Kind == "subtest" && item.Name == "standalone")
	}
	if !test || !subtest {
		t.Fatalf("active suite test boundaries missing: test=%t subtest=%t", test, subtest)
	}
}

func repositoryFile(t *testing.T, path string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller path unavailable")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../.."))
	content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
