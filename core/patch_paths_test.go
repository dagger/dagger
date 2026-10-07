package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePatchPaths(t *testing.T) {
	t.Run("hand-written", func(t *testing.T) {
		for _, tc := range []struct {
			name, patch, wantErr string
			want                 []PatchFilePaths
		}{
			{
				name: "modify",
				patch: `diff --git a/dir/a.txt b/dir/a.txt
index 1234567..89abcde 100644
--- a/dir/a.txt
+++ b/dir/a.txt
@@ -1,2 +1,2 @@
 keep
-old
+new
`,
				want: []PatchFilePaths{{Old: "dir/a.txt", New: "dir/a.txt"}},
			},
			{
				name: "hunk lines that look like headers",
				patch: `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,3 +1,3 @@
--- not/a/header
-+++ nor/this
+diff --git a/x b/x
+@@ -1 +1 @@
 ctx
diff --git a/b.txt b/b.txt
new file mode 100644
--- /dev/null
+++ b/b.txt
@@ -0,0 +1 @@
+hi
`,
				want: []PatchFilePaths{
					{Old: "a.txt", New: "a.txt"},
					{New: "b.txt"},
				},
			},
			{
				name: "create, delete, rename, copy and quoted paths",
				patch: `From 123 Mon Sep 17 00:00:00 2001
Subject: commit text --- is not a header

diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..e69de29
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index e69de29..0000000
diff --git a/old name.txt b/new name.txt
similarity index 100%
rename from old name.txt
rename to new name.txt
diff --git a/src.txt b/dst.txt
similarity index 100%
copy from src.txt
copy to dst.txt
diff --git "a/caf\303\251 \"x\".txt" "b/caf\303\251 \"x\".txt"
index 1..2 100644
--- "a/caf\303\251 \"x\".txt"
+++ "b/caf\303\251 \"x\".txt"
@@ -1 +1 @@
-a
+b
diff --git a/with space.txt b/with space.txt
old mode 100644
new mode 100755
`,
				want: []PatchFilePaths{
					{New: "new.txt"},
					{Old: "gone.txt"},
					{Old: "old name.txt", New: "new name.txt"},
					{Old: "src.txt", New: "dst.txt", Copy: true},
					{Old: `café "x".txt`, New: `café "x".txt`},
					{Old: "with space.txt", New: "with space.txt"},
				},
			},
			{
				name: "headerless section after a git section",
				patch: `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-a
+b
--- a/other.txt
+++ b/other.txt
@@ -1 +1 @@
-c
+d
`,
				wantErr: `patch section for "other.txt" has no "diff --git" header`,
			},
			{
				name: "headerless section after a mode-only git section",
				patch: `diff --git a/a.txt b/a.txt
old mode 100644
new mode 100755
--- a/other.txt
+++ b/other.txt
@@ -1 +1 @@
-c
+d
`,
				wantErr: `names "other.txt", but its "diff --git" header names "a.txt"`,
			},
			{
				name:    "plain unified diff with timestamps",
				patch:   "--- a/x.txt\t2020-01-01 00:00:00\n+++ b/x.txt\t2020-01-02 00:00:00\n@@ -1 +1 @@\n-a\n+b\n",
				wantErr: `patch section for "x.txt" has no "diff --git" header; only Git-format patches are supported`,
			},
			{
				name:    "plain unified diff creating with an epoch timestamp",
				patch:   "--- a/x\t1970-01-01 00:00:00 +0000\n+++ b/x\t2020-01-02 00:00:00 +0000\n@@ -0,0 +1 @@\n+b\n",
				wantErr: `patch section for "x" has no "diff --git" header`,
			},
			{
				name:    "plain unified diff from /dev/null",
				patch:   "--- /dev/null\n+++ b/x\n@@ -0,0 +1 @@\n+b\n",
				wantErr: `patch section for "/dev/null" has no "diff --git" header`,
			},
			{name: "empty", patch: "", want: nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := ParsePatchPaths([]byte(tc.patch))
				if tc.wantErr != "" {
					require.ErrorContains(t, err, tc.wantErr)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("git's own output", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		dir := t.TempDir()
		git := func(args ...string) []byte {
			cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "core.quotePath=true"}, args...)...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			return out
		}
		write := func(rel, contents string) {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(contents), 0o644))
		}
		git("init", "-q")
		big := ""
		for i := range 50 {
			big += "line " + string(rune('a'+i%26)) + "\n"
		}
		write("keep/mod.txt", "one\ntwo\n")
		write("keep/del.txt", "bye\n")
		write("ren/from.txt", big)
		write("bin.dat", "\x00\x01binary")
		git("add", ".")
		git("commit", "-qm", "base")
		write("keep/mod.txt", "one\nthree\n")
		require.NoError(t, os.Remove(filepath.Join(dir, "keep/del.txt")))
		require.NoError(t, os.Rename(filepath.Join(dir, "ren/from.txt"), filepath.Join(dir, "ren/to file.txt")))
		write("bin.dat", "\x00\x02binary changed")
		write("añadido.txt", "new\n")
		git("add", "-A")
		patch := git("diff", "--cached", "--binary", "-M")

		got, err := ParsePatchPaths(patch)
		require.NoError(t, err)
		require.ElementsMatch(t, []PatchFilePaths{
			{Old: "bin.dat", New: "bin.dat"},
			{Old: "keep/del.txt"},
			{Old: "keep/mod.txt", New: "keep/mod.txt"},
			{Old: "ren/from.txt", New: "ren/to file.txt"},
			{New: "añadido.txt"},
		}, got)

		// Sections split the patch whole, each one from its own header.
		sections, err := ParsePatchSections(patch)
		require.NoError(t, err)
		require.Len(t, sections, len(got))
		var joined []byte
		for i, section := range sections {
			require.Equal(t, got[i], section.PatchFilePaths)
			require.True(t, bytes.HasPrefix(section.Patch, []byte("diff --git ")), string(section.Patch))
			reparsed, err := ParsePatchPaths(section.Patch)
			require.NoError(t, err)
			require.Equal(t, []PatchFilePaths{got[i]}, reparsed)
			joined = append(joined, section.Patch...)
		}
		require.Equal(t, string(patch), string(joined))
	})

	t.Run("sections skip a preamble", func(t *testing.T) {
		patch := "commit message\n\ndiff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n" +
			"diff --git a/y b/y\r\nnew file mode 100644\r\n--- /dev/null\r\n+++ b/y\r\n@@ -0,0 +1 @@\r\n+y"
		sections, err := ParsePatchSections([]byte(patch))
		require.NoError(t, err)
		require.Len(t, sections, 2)
		require.Equal(t, PatchFilePaths{Old: "x", New: "x"}, sections[0].PatchFilePaths)
		require.Equal(t, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n", string(sections[0].Patch))
		require.Equal(t, PatchFilePaths{New: "y"}, sections[1].PatchFilePaths)
		require.Equal(t, "diff --git a/y b/y\r\nnew file mode 100644\r\n--- /dev/null\r\n+++ b/y\r\n@@ -0,0 +1 @@\r\n+y", string(sections[1].Patch))
	})
}
