package dagui

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	manyTests := []string{"TestA", "TestB", "TestC"}
	for range 334 {
		manyTests = append(manyTests, "TestMore")
	}
	many := "dag://?check=go/packages/tests/test&go-package=cloud/components"
	for _, test := range manyTests {
		many += "&go-test=" + test
	}

	tests := []struct {
		name    string
		in      string
		want    *Address
		compact string
	}{
		{
			name:    "check type key",
			in:      "dag://?check=test-split/test-base",
			want:    &Address{Kind: "check", Path: "test-split/test-base"},
			compact: "test-split/test-base",
		},
		{
			name:    "generator type key",
			in:      "dag://?generator=go/generate",
			want:    &Address{Kind: "generator", Path: "go/generate"},
			compact: "go/generate",
		},
		{
			name: "collection item",
			in:   "dag://?check=verify&view-package=left&view-case=Bar",
			want: &Address{Kind: "check", Path: "verify", Dimensions: []AddressDimension{
				{Name: "view-package", Keys: []string{"left"}},
				{Name: "view-case", Keys: []string{"Bar"}},
			}},
			compact: "verify view-package=left view-case=Bar",
		},
		{
			name: "repeated keys",
			in:   many,
			want: &Address{Kind: "check", Path: "go/packages/tests/test", Dimensions: []AddressDimension{
				{Name: "go-package", Keys: []string{"cloud/components"}},
				{Name: "go-test", Keys: manyTests},
			}},
			compact: "go/packages/tests/test go-package=cloud/components go-test×337",
		},
		{
			name: "module and name columns",
			in:   "dag+check://?module=golang&name=modules.packages.tests.test&golang-package=cmd/init",
			want: &Address{Types: []string{"check"}, Kind: "check", Path: "golang/modules/packages/tests/test", Module: "golang", Dimensions: []AddressDimension{
				{Name: "golang-package", Keys: []string{"cmd/init"}},
			}},
			compact: "golang/modules/packages/tests/test golang-package=cmd/init",
		},
		{
			name: "name column beside a collection named for the scheme's type",
			in:   "dag+go-test://?module=go&name=modules.tests&go-module=./api&go-test=TestHealth",
			want: &Address{Types: []string{"go-test"}, Kind: "go-test", Path: "go/modules/tests", Module: "go", Dimensions: []AddressDimension{
				{Name: "go-module", Keys: []string{"./api"}},
				{Name: "go-test", Keys: []string{"TestHealth"}},
			}},
			compact: "go/modules/tests go-module=./api go-test=TestHealth",
		},
		{
			name:    "name column alone",
			in:      "dag://?name=python310.unit",
			want:    &Address{Path: "python310/unit"},
			compact: "python310/unit",
		},
		{
			name: "collection keys alone",
			in:   "dag+generator://?golang-package=cmd/dagger",
			want: &Address{Types: []string{"generator"}, Kind: "generator", Dimensions: []AddressDimension{
				{Name: "golang-package", Keys: []string{"cmd/dagger"}},
			}},
			compact: "golang-package=cmd/dagger",
		},
		{
			name:    "path form",
			in:      "dag://go/test-all",
			want:    &Address{Path: "go/test-all"},
			compact: "go/test-all",
		},
		{
			name:    "typed path form",
			in:      "dag+check://go/test-all",
			want:    &Address{Types: []string{"check"}, Kind: "check", Path: "go/test-all"},
			compact: "go/test-all",
		},
		{
			name: "qualified artifact type key",
			in:   "dag+go-test://?artifact-go-test=go/modules/tests&go-module=./api&go-test=TestHealth",
			want: &Address{Types: []string{"go-test"}, Kind: "go-test", Path: "go/modules/tests", Dimensions: []AddressDimension{
				{Name: "go-module", Keys: []string{"./api"}},
				{Name: "go-test", Keys: []string{"TestHealth"}},
			}},
			compact: "go/modules/tests go-module=./api go-test=TestHealth",
		},
		{
			name:    "type dimension",
			in:      "dag://?type:GoModule=go/modules",
			want:    &Address{Kind: "go-module", Path: "go/modules"},
			compact: "go/modules",
		},
		{
			name: "absolute",
			in:   "dag://github.com/dagger/dagger@main:go/test?go-module=.",
			want: &Address{Workspace: "github.com/dagger/dagger", Version: "main", Path: "go/test", Dimensions: []AddressDimension{
				{Name: "go-module", Keys: []string{"."}},
			}},
			compact: "github.com/dagger/dagger@main go/test go-module=.",
		},
		{
			name:    "old colon separator",
			in:      "dag://go:test",
			want:    &Address{Path: "go/test"},
			compact: "go/test",
		},
		{
			name: "escaped keys and keyless dimension",
			in:   "dag://?check=lint&path=a%26b%3Dc&go-test",
			want: &Address{Kind: "check", Path: "lint", Dimensions: []AddressDimension{
				{Name: "path", Keys: []string{"a&b=c"}},
				{Name: "go-test", Any: true},
			}},
			compact: "lint path=a&b=c go-test=*",
		},
		{
			name: "absent dimension",
			in:   "dag+check://grid/generate/stale?!cell&row=1",
			want: &Address{Types: []string{"check"}, Kind: "check", Path: "grid/generate/stale", Dimensions: []AddressDimension{
				{Name: "cell", Absent: true},
				{Name: "row", Keys: []string{"1"}},
			}},
			compact: "grid/generate/stale !cell row=1",
		},
		{
			name: "key holding a URL",
			in:   "dag://?check=fetch&url=https://example.com/x",
			want: &Address{Kind: "check", Path: "fetch", Dimensions: []AddressDimension{
				{Name: "url", Keys: []string{"https://example.com/x"}},
			}},
			compact: "fetch url=https://example.com/x",
		},
		{name: "plain name", in: "go/test-all"},
		{name: "function name", in: "testAll"},
		{name: "other scheme", in: "https://example.com/?check=x"},
		{name: "scheme lookalike", in: "dagger://?check=x"},
		{name: "empty address", in: "dag://"},
		{name: "empty scheme type", in: "dag+://?check=x"},
		{name: "bad escape", in: "dag://?check=%zz"},
		{name: "absent dimension with a key", in: "dag://?check=x&!cell=a"},
		{name: "absent nothing", in: "dag://?check=x&!"},
		{name: "absolute without path separator", in: "dag://github.com/x@main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAddress(tt.in)
			if tt.want == nil {
				if ok {
					t.Fatalf("ParseAddress(%q) = %+v, want not an address", tt.in, got)
				}
				return
			}
			if !ok {
				t.Fatalf("ParseAddress(%q) failed", tt.in)
			}
			tt.want.Raw = tt.in
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseAddress(%q) =\n%+v\nwant\n%+v", tt.in, got, tt.want)
			}
			if c := got.Compact(); c != tt.compact {
				t.Errorf("Compact() = %q, want %q", c, tt.compact)
			}
		})
	}
}

func TestAddressCompactWithout(t *testing.T) {
	for _, tt := range []struct {
		in   string
		hide []string
		want string
	}{
		{"dag://?check=lint&module=golang&golang-module=.", []string{"module"}, "lint golang-module=."},
		{"dag+check://?module=golang&name=modules.lint&golang-module=.", []string{"module"}, "modules/lint golang-module=."},
		{"dag+check://?module=golang&name=modules.lint&golang-module=.", []string{"golang-module"}, "golang/modules/lint"},
		{"dag://?check=lint&go-test=TestA&go-test=TestB", nil, "lint go-test×2"},
	} {
		addr, ok := ParseAddress(tt.in)
		if !ok {
			t.Fatalf("ParseAddress(%q) failed", tt.in)
		}
		if got := addr.CompactWithout(tt.hide); got != tt.want {
			t.Errorf("CompactWithout(%q, %v) = %q, want %q", tt.in, tt.hide, got, tt.want)
		}
	}
}

func TestAddressSegments(t *testing.T) {
	addr, ok := ParseAddress("dag://?check=test-split/test-base")
	if !ok {
		t.Fatal("not parsed")
	}
	if got := strings.Join(addr.Segments(), "|"); got != "test-split|test-base" {
		t.Errorf("Segments() = %q", got)
	}
}

func TestElideSegments(t *testing.T) {
	for _, tt := range []struct {
		path string
		want string
	}{
		{"go/test-all", "go/test-all"},
		{"go/packages/tests/test", "go/packages/tests/test"},
		{"a/b/c/d/e/f/g", "a/b/c/d/e/f/g"},
		{
			"some/really/deeply/nested/module/path/with/many/segments/lint",
			"some/…/module/path/with/many/segments/lint",
		},
		{
			"a/" + strings.Repeat("x", 60) + "/b/c",
			"a/…/b/c",
		},
		{
			"first/" + strings.Repeat("y", 60) + "/" + strings.Repeat("z", 50),
			"first/" + strings.Repeat("y", 60) + "/" + strings.Repeat("z", 50),
		},
	} {
		got := strings.Join(ElideSegments(strings.Split(tt.path, "/"), 48), "/")
		if got != tt.want {
			t.Errorf("ElideSegments(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestKebabCase(t *testing.T) {
	for in, want := range map[string]string{
		"Check":     "check",
		"GoTest":    "go-test",
		"HTTPProbe": "http-probe",
		"Go2Module": "go2-module",
	} {
		if got := kebabCase(in); got != want {
			t.Errorf("kebabCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// Artifacts listed together hide the columns they all share, as a table does.
func TestConstantDimensions(t *testing.T) {
	got := ConstantDimensions([]string{
		"dag://?check=lint&module=golang&golang-module=.",
		"dag://?check=test&module=golang&golang-module=./sdk",
		"not an address",
	})
	if !slices.Equal(got, []string{"module"}) {
		t.Errorf("constant = %v, want [module]", got)
	}
	// A name column's module, folded into the path, still compares.
	got = ConstantDimensions([]string{
		"dag://?check=lint&module=golang&golang-module=.",
		"dag+check://?module=golang&name=modules.packages.tests.test&golang-package=cmd/init",
	})
	if !slices.Equal(got, []string{"module"}) {
		t.Errorf("constant = %v, want [module]", got)
	}
	// No key and any key are different selections.
	got = ConstantDimensions([]string{
		"dag://?check=stale&!cell&module=grid",
		"dag://?check=stale&cell&module=grid",
	})
	if !slices.Equal(got, []string{"module"}) {
		t.Errorf("constant = %v, want [module]", got)
	}
	if got := ConstantDimensions([]string{"dag://?check=lint&module=golang"}); got != nil {
		t.Errorf("a lone artifact has no columns to compare, got %v", got)
	}
}
