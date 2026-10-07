package core

import (
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
// from/to`, `copy from/to`, `new file`, `deleted file`) and each section's
// `---`/`+++` lines, git's C-quoted paths and /dev/null included. Hunk bodies
// and binary payloads are skipped, never read as headers.
//
// Only Git-format patches are accepted: a file section without a `diff --git`
// header is an error. git reads a traditional unified diff's paths by rules
// of its own (an epoch timestamp marks a creation; differing ---/+++ names
// are one file, not a rename), and callers rely on these paths agreeing with
// what `git apply` touches.
func ParsePatchPaths(patch []byte) ([]PatchFilePaths, error) {
	sections, err := ParsePatchSections(patch)
	if err != nil || len(sections) == 0 {
		return nil, err
	}
	files := make([]PatchFilePaths, len(sections))
	for i, section := range sections {
		files[i] = section.PatchFilePaths
	}
	return files, nil
}

// PatchSection is one file section of a patch: the paths it names, as
// ParsePatchPaths reads them, and its own bytes, from its `diff --git` line
// up to the next section's. Concatenating sections in order makes a patch of
// just those files.
type PatchSection struct {
	PatchFilePaths
	Patch []byte
}

// ParsePatchSections is ParsePatchPaths with each section's bytes, for
// splitting a patch by file. Anything before the first section, such as a
// commit message, belongs to none.
func ParsePatchSections(patch []byte) ([]PatchSection, error) {
	p := patchPathParser{patch: patch}
	for p.offset < len(patch) {
		line, next := patch[p.offset:], len(patch)
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, next = line[:i], p.offset+i+1
		}
		// As bufio.ScanLines reads lines.
		line = bytes.TrimSuffix(line, []byte("\r"))
		if err := p.line(string(line)); err != nil {
			return nil, err
		}
		p.offset = next
	}
	p.flush()
	return p.files, nil
}

type patchPathParser struct {
	files []PatchSection
	cur   *PatchFilePaths
	// patch is the whole patch; offset is where the line being read starts,
	// and start where cur's section does.
	patch         []byte
	offset, start int
	// created and deleted are what the headers said about cur.
	created, deleted bool
	// body is set once cur has had its +++ line or a hunk: a --- line after
	// that opens another section.
	body bool
	// hunkOld and hunkNew are the lines left in the current hunk.
	hunkOld, hunkNew int
}

func (p *patchPathParser) flush() {
	if p.cur == nil {
		return
	}
	if p.created {
		p.cur.Old = ""
	}
	if p.deleted {
		p.cur.New = ""
	}
	if p.cur.Old != "" || p.cur.New != "" {
		p.files = append(p.files, PatchSection{
			PatchFilePaths: *p.cur,
			Patch:          p.patch[p.start:p.offset],
		})
	}
	p.cur, p.created, p.deleted, p.body = nil, false, false, false
}

func (p *patchPathParser) line(line string) error {
	if p.hunkOld > 0 || p.hunkNew > 0 {
		switch {
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file"
		case strings.HasPrefix(line, "-"):
			p.hunkOld--
		case strings.HasPrefix(line, "+"):
			p.hunkNew--
		default:
			p.hunkOld--
			p.hunkNew--
		}
		return nil
	}
	var err error
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.flush()
		p.start = p.offset
		var old, new string
		if old, new, err = parseDiffGitHeader(strings.TrimPrefix(line, "diff --git ")); err == nil {
			p.cur = &PatchFilePaths{Old: old, New: new}
		}
	case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		err = p.unifiedPath(line)
	case strings.HasPrefix(line, "@@ "):
		p.hunkOld, p.hunkNew, err = parseHunkHeader(line)
		p.body = true
	case p.cur != nil:
		err = p.extendedHeader(line)
	}
	return err
}

// unifiedPath reads a ---/+++ line.
func (p *patchPathParser) unifiedPath(line string) error {
	path, err := parseUnifiedPath(line[len("--- "):])
	if err != nil {
		return err
	}
	old := strings.HasPrefix(line, "--- ")
	// A section opens with its `diff --git` line. A ---/+++ line outside one,
	// or a --- line after the current one's +++ line or hunks, opens a
	// traditional unified diff section: refuse it (see ParsePatchPaths).
	if p.cur == nil || p.body {
		name := path
		if name == "" {
			name = "/dev/null"
		}
		return fmt.Errorf("patch section for %q has no \"diff --git\" header; only Git-format patches are supported", name)
	}
	if !old {
		p.body = true
	}
	// git refuses ---/+++ names that differ from the section's own, so
	// neither may a headerless section's lines take over one with no hunks.
	known := p.cur.New
	if old {
		known = p.cur.Old
	}
	if path != "" && known != "" && path != known {
		return fmt.Errorf("patch line %q names %q, but its \"diff --git\" header names %q", line, path, known)
	}
	switch {
	case old && path == "":
		p.created = true
	case old:
		p.cur.Old = path
	case path == "":
		p.deleted = true
	default:
		p.cur.New = path
	}
	return nil
}

// extendedHeader reads one of git's extended header lines.
func (p *patchPathParser) extendedHeader(line string) error {
	switch {
	case strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "copy from "):
		path, err := unquotePatchPath(line[strings.Index(line, " from ")+len(" from "):])
		if err != nil {
			return err
		}
		p.cur.Old = path
		p.cur.Copy = strings.HasPrefix(line, "copy ")
	case strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
		path, err := unquotePatchPath(line[strings.Index(line, " to ")+len(" to "):])
		if err != nil {
			return err
		}
		p.cur.New = path
	case strings.HasPrefix(line, "new file mode "):
		p.created = true
	case strings.HasPrefix(line, "deleted file mode "):
		p.deleted = true
	}
	return nil
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
