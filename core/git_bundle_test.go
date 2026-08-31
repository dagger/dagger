package core

import (
	"crypto/sha1" //nolint:gosec // Git pack checksum
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGitBundleHeader(t *testing.T) {
	t.Run("version 2", func(t *testing.T) {
		sha := strings.Repeat("1", sha1.Size*2)
		bundle, offset, err := parseGitBundleHeader(strings.NewReader(
			"# v2 git bundle\n" + sha + " refs/heads/main\n\nPACK"))
		require.NoError(t, err)
		require.Equal(t, 2, bundle.Version)
		require.Equal(t, "sha1", bundle.ObjectFormat)
		require.Equal(t, []*GitBundleRef{{Name: "refs/heads/main", SHA: sha}}, bundle.Refs)
		require.Empty(t, bundle.PrerequisiteSHAs)
		require.Equal(t, int64(len("# v2 git bundle\n"+sha+" refs/heads/main\n\n")), offset)
	})

	t.Run("version 3 sha256 with prerequisite", func(t *testing.T) {
		base := strings.Repeat("a", sha256.Size*2)
		head := strings.Repeat("b", sha256.Size*2)
		bundle, _, err := parseGitBundleHeader(strings.NewReader(
			"# v3 git bundle\n@object-format=sha256\n-" + base + " base commit\n" + head + " refs/heads/main\n\nPACK"))
		require.NoError(t, err)
		require.Equal(t, 3, bundle.Version)
		require.Equal(t, "sha256", bundle.ObjectFormat)
		require.Equal(t, []string{base}, bundle.PrerequisiteSHAs)
		require.Equal(t, []*GitBundleRef{{Name: "refs/heads/main", SHA: head}}, bundle.Refs)
	})

	t.Run("unsupported capability", func(t *testing.T) {
		sha := strings.Repeat("1", sha1.Size*2)
		_, _, err := parseGitBundleHeader(strings.NewReader(
			"# v3 git bundle\n@filter=blob:none\n" + sha + " refs/heads/main\n\nPACK"))
		require.ErrorContains(t, err, `unsupported git bundle capability "filter"`)
	})

	sha := strings.Repeat("1", sha1.Size*2)
	for name, input := range map[string]string{
		"bad signature":        "not a bundle\n",
		"truncated header":     "# v3 git bundle\n" + sha + " refs/heads/main\n",
		"bad object format":    "# v3 git bundle\n@object-format=md5\n" + sha + " refs/heads/main\n\n",
		"bad object id":        "# v3 git bundle\n123 refs/heads/main\n\n",
		"no refs":              "# v3 git bundle\n-" + sha + " base\n\n",
		"duplicate ref":        "# v3 git bundle\n" + sha + " refs/heads/main\n" + sha + " refs/heads/main\n\n",
		"late capability":      "# v3 git bundle\n" + sha + " refs/heads/main\n@object-format=sha1\n\n",
		"v2 with capability":   "# v2 git bundle\n@object-format=sha1\n" + sha + " refs/heads/main\n\n",
		"missing ref name":     "# v3 git bundle\n" + sha + "\n\n",
		"duplicate capability": "# v3 git bundle\n@object-format=sha1\n@object-format=sha1\n" + sha + " refs/heads/main\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseGitBundleHeader(strings.NewReader(input))
			require.Error(t, err)
		})
	}
}

func TestParseGitBundleHeaderRefLimit(t *testing.T) {
	sha := strings.Repeat("1", sha1.Size*2)
	var header strings.Builder
	header.WriteString("# v3 git bundle\n")
	for i := 0; i <= MaxGitBundleRefs; i++ {
		fmt.Fprintf(&header, "%s refs/heads/ref-%d\n", sha, i)
	}
	header.WriteString("\n")
	_, _, err := parseGitBundleHeader(strings.NewReader(header.String()))
	require.ErrorContains(t, err, "more than")
	require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleRefs))
}

func TestInspectGitBundleFileSafeguards(t *testing.T) {
	sha := strings.Repeat("1", sha1.Size*2)
	header := []byte("# v3 git bundle\n" + sha + " refs/heads/main\n\n")
	valid := testGitBundleBytes(header, 0, nil, "sha1")

	t.Run("valid envelope", func(t *testing.T) {
		path := writeTestGitBundle(t, valid)
		parsed, err := inspectGitBundleFile(path)
		require.NoError(t, err)
		require.Equal(t, 3, parsed.Version)
	})

	t.Run("truncated", func(t *testing.T) {
		path := writeTestGitBundle(t, valid[:len(valid)-1])
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "truncated")
	})

	t.Run("corrupt", func(t *testing.T) {
		corrupt := append([]byte(nil), valid...)
		corrupt[len(header)+12] ^= 0xff
		path := writeTestGitBundle(t, corrupt)
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "checksum")
	})

	t.Run("object count", func(t *testing.T) {
		tooMany := testGitBundleBytes(header, MaxGitBundleObjects+1, nil, "sha1")
		path := writeTestGitBundle(t, tooMany)
		_, err := inspectGitBundleFile(path)
		require.ErrorContains(t, err, "object count")
		require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleObjects))
	})

	t.Run("file size", func(t *testing.T) {
		path := t.TempDir() + "/large.bundle"
		file, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(MaxGitBundleBytes+1))
		require.NoError(t, file.Close())
		_, err = inspectGitBundleFile(path)
		require.ErrorContains(t, err, "size")
		require.ErrorContains(t, err, fmt.Sprint(MaxGitBundleBytes))
	})
}

func testGitBundleBytes(header []byte, objectCount uint32, objectData []byte, objectFormat string) []byte {
	pack := make([]byte, 12, 12+len(objectData)+sha256.Size)
	copy(pack, "PACK")
	binary.BigEndian.PutUint32(pack[4:8], 2)
	binary.BigEndian.PutUint32(pack[8:12], objectCount)
	pack = append(pack, objectData...)
	if objectFormat == "sha256" {
		sum := sha256.Sum256(pack)
		pack = append(pack, sum[:]...)
	} else {
		sum := sha1.Sum(pack) //nolint:gosec // Git pack checksum
		pack = append(pack, sum[:]...)
	}
	return append(append([]byte(nil), header...), pack...)
}

func writeTestGitBundle(t *testing.T, contents []byte) string {
	t.Helper()
	path := t.TempDir() + "/test.bundle"
	require.NoError(t, os.WriteFile(path, contents, 0o600))
	return path
}
