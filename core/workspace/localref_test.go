package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsLocalRef(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		source  string
		pin     string
		isLocal bool
	}{
		{name: "empty", source: "", isLocal: true},
		{name: "cwd", source: ".", isLocal: true},
		{name: "explicit relative", source: "./mymod", isLocal: true},
		{name: "parent", source: "..", isLocal: true},
		{name: "explicit parent", source: "../libs/mymod", isLocal: true},
		{name: "absolute", source: "/srv/workspace/mymod", isLocal: true},
		{name: "bare name", source: "mymod", isLocal: true},
		{name: "nested path", source: "path/to/mymod", isLocal: true},
		{name: "leading dot segment", source: ".dagger/mymod", isLocal: true},
		{name: "dot segment below a subdirectory", source: "common/.dagger/mymod", isLocal: true},
		{name: "dot segment deeply nested", source: "a/b/c/.dagger/mymod", isLocal: true},
		{name: "dotted leaf below a subdirectory", source: "common/mymod.v2", isLocal: true},
		// A path typed on Windows: no git ref separates with backslashes.
		{name: "windows dot segment below a subdirectory", source: `common\.dagger\mymod`, isLocal: true},
		{name: "windows dotted leaf", source: `common\mymod.v2`, isLocal: true},

		{name: "github", source: "github.com/acme/mymod", isLocal: false},
		{name: "github subdir", source: "github.com/acme/repo/sub/mymod", isLocal: false},
		{name: "github with version", source: "github.com/acme/mymod@v1.2.3", isLocal: false},
		{name: "gitlab with branch", source: "gitlab.com/acme/mymod@main", isLocal: false},
		{name: "scp-style", source: "git@github.com:acme/mymod", isLocal: false},
		{name: "scp-style dot-free host", source: "git@internal:acme/mymod.git", isLocal: false},
		{name: "userless scp-style dot-free host", source: "internal:2222/acme/mymod.git", isLocal: false},
		{name: "https scheme", source: "https://github.com/acme/mymod", isLocal: false},
		{name: "http scheme", source: "http://github.com/acme/mymod", isLocal: false},
		{name: "ssh scheme", source: "ssh://git@github.com/acme/mymod", isLocal: false},
		{name: "bare host", source: "github.com", isLocal: false},
		{name: "dotted first segment", source: "mymod.v2", isLocal: false},
		{name: "azure devops services", source: "dev.azure.com/acme/public/_git/mymod", isLocal: false},
		{name: "azure devops server on-prem", source: "azdo/tfs/acme/public/_git/mymod.git", isLocal: false},
		// "_git" belongs to Azure's on-prem shorthand, so a dotted path under a
		// directory of that name reads remote — as it did before this rule.
		{name: "dotted path below a _git segment", source: "src/_git/mymod.git", isLocal: false},

		{name: "pin wins over a bare name", source: "mymod", pin: "abcdef0", isLocal: false},
		{name: "pin wins over a relative path", source: "./mymod", pin: "abcdef0", isLocal: false},
		{name: "pin wins over a dot segment", source: "common/.dagger/mymod", pin: "abcdef0", isLocal: false},

		// A dot-free host is indistinguishable from a bare local path without
		// a scheme; callers disambiguate with `ssh://` or `https://`.
		{name: "dot-free host reads as local", source: "localhost/acme/mymod", isLocal: true},
		{name: "dot-free host reads as local, dotted leaf", source: "localhost/acme/mymod.git", isLocal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.isLocal, IsLocalRef(tc.source, tc.pin))
		})
	}
}
