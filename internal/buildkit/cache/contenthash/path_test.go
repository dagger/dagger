package contenthash

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootPathSymlinks(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	// Make the following tree:
	//  /
	//  |- target/
	mkdirAll(t, tmpdir, "target")
	//  |- link1
	//     |- sub/
	//     |- notaloop -> ../target
	//     |- final -> /target
	mkdirAll(t, tmpdir, "link1")
	mkdirAll(t, tmpdir, "link1/sub")
	symlink(t, tmpdir, "link1/notaloop", "../target")
	symlink(t, tmpdir, "link1/final", "/target")
	//  |- link2
	//     |- link1 -> /link1/
	//     |- link1sub -> /link1/sub/
	//     |- notaloop -> ./link1sub/../notaloop
	//     |- notaloop_abs -> /link2/link1sub/../notaloop
	//     |- notaloop2 -> ./link1/../link1/notaloop
	//     |- notaloop2_abs -> /link2/link1/../link1/notaloop
	//     |- target -> ./link1sub/../final
	//     |- target_abs -> /link2/link1sub/../final
	//     |- target2 -> ./link1/../link1/final
	//     |- target2_abs -> /link2/link1/../link1/final
	mkdirAll(t, tmpdir, "link2")
	symlink(t, tmpdir, "link2/link1", "/link1/")
	symlink(t, tmpdir, "link2/link1sub", "/link1/sub/")
	symlink(t, tmpdir, "link2/notaloop", "./link1sub/../notaloop")
	symlink(t, tmpdir, "link2/notaloop_abs", "/link2/link1sub/../notaloop")
	symlink(t, tmpdir, "link2/notaloop2", "./link1/../link1/notaloop")
	symlink(t, tmpdir, "link2/notaloop2_abs", "/link2/link1/../link1/notaloop")
	symlink(t, tmpdir, "link2/target", "./link1sub/../final")
	symlink(t, tmpdir, "link2/target_abs", "/link2/link1sub/../final")
	symlink(t, tmpdir, "link2/target2", "./link1/../link1/final")
	symlink(t, tmpdir, "link2/target2_abs", "/link2/link1/../link1/final")
	//  |- link3 -> /link2
	symlink(t, tmpdir, "link3", "/link2")

	for _, test := range []struct {
		path           string
		followTrailing bool
		expected       string
	}{
		{"target", true, "/target"},
		{"target", false, "/target"},
		{"link1/notaloop", true, "/target"},
		{"link1/notaloop", false, "/link1/notaloop"},
		{"link1/final", true, "/target"},
		{"link1/final", false, "/link1/final"},
		{"link2/notaloop", true, "/target"},
		{"link2/notaloop", false, "/link2/notaloop"},
		{"link2/notaloop_abs", true, "/target"},
		{"link2/notaloop_abs", false, "/link2/notaloop_abs"},
		{"link2/notaloop2", true, "/target"},
		{"link2/notaloop2", false, "/link2/notaloop2"},
		{"link2/notaloop2_abs", true, "/target"},
		{"link2/notaloop2_abs", false, "/link2/notaloop2_abs"},
		{"link2/target", true, "/target"},
		{"link2/target", false, "/link2/target"},
		{"link2/target_abs", true, "/target"},
		{"link2/target_abs", false, "/link2/target_abs"},
		{"link2/target2", true, "/target"},
		{"link2/target2", false, "/link2/target2"},
		{"link2/target2_abs", true, "/target"},
		{"link2/target2_abs", false, "/link2/target2_abs"},
		{"link2/link1sub/../notaloop", true, "/target"},             // link2/notaloop
		{"link2/link1sub/../notaloop", false, "/link1/notaloop"},    // link2/notaloop
		{"link2/link1/../link1/notaloop", true, "/target"},          // link2/notaloop2
		{"link2/link1/../link1/notaloop", false, "/link1/notaloop"}, // link2/notaloop2
		{"link2/link1sub/../final", true, "/target"},                // link2/target
		{"link2/link1sub/../final", false, "/link1/final"},          // link2/target
		{"link2/link1/../link1/final", true, "/target"},             // link2/target2
		{"link2/link1/../link1/final", false, "/link1/final"},       // link2/target2
		{"link3/target", true, "/target"},
		{"link3/target", false, "/link2/target"},
		{"link3/target_abs", true, "/target"},
		{"link3/target_abs", false, "/link2/target_abs"},
		{"link3/target2", true, "/target"},
		{"link3/target2", false, "/link2/target2"},
		{"link3/target2_abs", true, "/target"},
		{"link3/target2_abs", false, "/link2/target2_abs"},
		{"link3/link1sub/../notaloop", true, "/target"},             // link3/notaloop
		{"link3/link1sub/../notaloop", false, "/link1/notaloop"},    // link3/notaloop
		{"link3/link1/../link1/notaloop", true, "/target"},          // link3/notaloop2
		{"link3/link1/../link1/notaloop", false, "/link1/notaloop"}, // link3/notaloop2
		{"link3/link1sub/../final", true, "/target"},                // link3/target
		{"link3/link1sub/../final", false, "/link1/final"},          // link3/target
		{"link3/link1/../link1/final", true, "/target"},             // link3/target2
		{"link3/link1/../link1/final", false, "/link1/final"},       // link3/target2
	} {
		test := test // capture range variable
		t.Run(fmt.Sprintf("resolve(%q,followTrailing=%v)", test.path, test.followTrailing), func(t *testing.T) {
			t.Parallel()

			resolvedPath, err := rootPath(tmpdir, test.path, test.followTrailing, nil)
			require.NoError(t, err)

			expectedPath := filepath.Join(tmpdir, filepath.FromSlash(test.expected))
			require.Equal(t, expectedPath, resolvedPath)
		})
	}
}

func mkdirAll(t *testing.T, root, path string) {
	path = filepath.FromSlash(path)

	err := os.MkdirAll(filepath.Join(root, path), 0755)
	require.NoError(t, err)
}

func symlink(t *testing.T, root, linkname, target string) {
	linkname = filepath.FromSlash(linkname)

	// We need to add a dummy drive letter to emulate absolute symlinks on
	// Windows.
	if runtime.GOOS == "windows" && path.IsAbs(target) {
		target = "Z:" + filepath.FromSlash(target)
	} else {
		target = filepath.FromSlash(target)
	}

	dir, _ := filepath.Split(linkname)
	mkdirAll(t, root, dir)

	fullLinkname := filepath.Join(root, linkname)

	err := os.Symlink(target, fullLinkname)
	require.NoError(t, err)

	// Windows seems to automatically change our /foo/../bar symlinks to /bar,
	// causing some tests to fail. Technically we only care about this symlink
	// behaviour on Linux (since we implemented it this way to keep Linux
	// compatibility), so if our symlink has the wrong target we can just skip
	// the test.
	actualTarget, err := os.Readlink(fullLinkname)
	require.NoError(t, err)
	if actualTarget != target {
		fn := t.Skipf
		if runtime.GOOS != "windows" {
			fn = t.Fatalf
		}
		fn("created link had the wrong contents -- %s -> %s", target, actualTarget)
	}
}
