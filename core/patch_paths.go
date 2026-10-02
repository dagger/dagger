package core

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// PatchFilePaths are the paths one file section of a patch names, relative to
// the tree it applies to (`git apply`'s default -p1). Old is empty for a file
// the patch creates, New for one it deletes; they differ for a rename or a
// copy.
type PatchFilePaths struct {
	Old, New string
	// Copy marks a copy: Old is read, not removed.
	Copy bool
}

// ParsePatchPaths returns the paths each file section of a patch touches, in
// order, from its headers: git's extended headers (`diff --git`, `rename
// from/to`, `copy from/to`, `new file`, `deleted file`) and the `---`/`+++`
// lines of any unified diff, git's C-quoted paths and /dev/null included.
// Hunk bodies and binary payloads are skipped, never read as headers.
func ParsePatchPaths(patch []byte) ([]PatchFilePaths, error) {
	var files []PatchFilePaths
	var cur *PatchFilePaths
	// created and deleted are what the extended headers said about cur.
	var created, deleted bool
	flush := func() {
		if cur == nil {
			return
		}
		if created {
			cur.Old = ""
		}
		if deleted {
			cur.New = ""
		}
		if cur.Old != "" || cur.New != "" {
			files = append(files, *cur)
		}
		cur, created, deleted = nil, false, false
	}

	sc := bufio.NewScanner(bytes.NewReader(patch))
	sc.Buffer(make([]byte, 0, 64*1024), len(patch)+1)
	// Lines left in the current hunk, old and new side.
	var hunkOld, hunkNew int
	for sc.Scan() {
		line := sc.Text()
		if hunkOld > 0 || hunkNew > 0 {
			switch {
			case strings.HasPrefix(line, "\\"):
				// "\ No newline at end of file"
			case strings.HasPrefix(line, "-"):
				hunkOld--
			case strings.HasPrefix(line, "+"):
				hunkNew--
			default:
				hunkOld--
				hunkNew--
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			old, new, err := parseDiffGitHeader(strings.TrimPrefix(line, "diff --git "))
			if err != nil {
				return nil, err
			}
			cur = &PatchFilePaths{Old: old, New: new}
		case strings.HasPrefix(line, "--- "):
			p, err := parseUnifiedPath(strings.TrimPrefix(line, "--- "))
			if err != nil {
				return nil, err
			}
			if cur == nil {
				// A plain unified diff: no `diff --git` line opens it.
				cur = &PatchFilePaths{}
			}
			if p == "" {
				created = true
			} else {
				cur.Old = p
			}
		case strings.HasPrefix(line, "+++ "):
			p, err := parseUnifiedPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, err
			}
			if cur == nil {
				cur = &PatchFilePaths{}
			}
			if p == "" {
				deleted = true
			} else {
				cur.New = p
			}
		case strings.HasPrefix(line, "@@ "):
			var err error
			hunkOld, hunkNew, err = parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
		case cur == nil:
			// Commit messages and other text around the diffs.
		case strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "copy from "):
			p, err := unquotePatchPath(line[strings.Index(line, " from ")+len(" from "):])
			if err != nil {
				return nil, err
			}
			cur.Old = p
			cur.Copy = strings.HasPrefix(line, "copy ")
		case strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
			p, err := unquotePatchPath(line[strings.Index(line, " to ")+len(" to "):])
			if err != nil {
				return nil, err
			}
			cur.New = p
		case strings.HasPrefix(line, "new file mode "):
			created = true
		case strings.HasPrefix(line, "deleted file mode "):
			deleted = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return files, nil
}

// parseDiffGitHeader splits the two paths of a `diff --git` line and strips
// their a/ and b/ prefixes. Unquoted paths may contain spaces; git only writes
// such a header when both names are the same, so it splits in the middle.
func parseDiffGitHeader(rest string) (old, new string, err error) {
	var a, b string
	if strings.HasPrefix(rest, `"`) {
		end := quotedEnd(rest)
		if end < 0 {
			return "", "", fmt.Errorf("malformed diff header %q", rest)
		}
		a, b = rest[:end], strings.TrimPrefix(rest[end:], " ")
	} else if i := strings.Index(rest, ` "`); i >= 0 {
		a, b = rest[:i], rest[i+1:]
	} else if mid := len(rest) / 2; len(rest)%2 == 1 && rest[mid] == ' ' && rest[2:mid] == rest[mid+3:] {
		a, b = rest[:mid], rest[mid+1:]
	} else if fields := strings.Split(rest, " "); len(fields) == 2 {
		a, b = fields[0], fields[1]
	} else {
		// Renames and copies name both sides in their extended headers;
		// anything else names them on its ---/+++ lines.
		return "", "", nil
	}
	if old, err = unquotePatchPath(a); err != nil {
		return "", "", err
	}
	if new, err = unquotePatchPath(b); err != nil {
		return "", "", err
	}
	return stripPatchPrefix(old), stripPatchPrefix(new), nil
}

// parseUnifiedPath parses the path of a ---/+++ line: "" for /dev/null, else
// the path without its a/ or b/ prefix or a trailing timestamp.
func parseUnifiedPath(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) {
		// A tab separates an optional timestamp.
		s, _, _ = strings.Cut(s, "\t")
	}
	p, err := unquotePatchPath(s)
	if err != nil {
		return "", err
	}
	if p == "/dev/null" {
		return "", nil
	}
	return stripPatchPrefix(p), nil
}

// parseHunkHeader returns the line counts of a "@@ -l,s +l,s @@" header.
func parseHunkHeader(line string) (old, new int, err error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, fmt.Errorf("malformed hunk header %q", line)
	}
	count := func(r string) (int, error) {
		_, n, ok := strings.Cut(r[1:], ",")
		if !ok {
			return 1, nil
		}
		return strconv.Atoi(n)
	}
	if old, err = count(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
	}
	if new, err = count(fields[2]); err != nil {
		return 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
	}
	return old, new, nil
}

// unquotePatchPath decodes git's C-style quoting of a path, if quoted.
func unquotePatchPath(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) {
		return s, nil
	}
	p, err := strconv.Unquote(s)
	if err != nil {
		return "", fmt.Errorf("malformed quoted path %s: %w", s, err)
	}
	return p, nil
}

// quotedEnd returns the index just past the closing quote of the quoted
// string s starts with, or -1.
func quotedEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// stripPatchPrefix strips the leading path component, as `git apply -p1`.
func stripPatchPrefix(p string) string {
	if _, rest, ok := strings.Cut(p, "/"); ok {
		return rest
	}
	return p
}
