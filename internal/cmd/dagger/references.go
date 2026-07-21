package daggercmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// workspaceReferencePrefix mirrors core.WorkspaceReferencePrefix: the reserved
// workspace-relative directory under which @-referenced host paths are mounted
// read-only. Kept as a local constant so reference plumbing on the client side
// doesn't need to import core; it is only used to render the workspace-relative
// path back to the user and the model.
const workspaceReferencePrefix = ".refs"

// referenceInfo records a host path the user attached with @, along with the
// read-only workspace path it was mounted at.
type referenceInfo struct {
	original string // path as typed after @, e.g. ~/foo/bar.txt
	mount    string // workspace-relative mount path, e.g. .refs/foo/bar.txt
	isDir    bool
}

// completeReferencePath completes an @-path against the host filesystem. frag
// is the text typed after the leading "@". A leading "~" is expanded to the
// home directory for listing, but the inserted text preserves the user's typed
// prefix (e.g. "~/") so the token round-trips.
func completeReferencePath(frag string) []tuist.Completion {
	dirPart, base := frag, ""
	if idx := strings.LastIndex(frag, "/"); idx >= 0 {
		dirPart, base = frag[:idx+1], frag[idx+1:]
	} else {
		dirPart, base = "", frag
	}

	listDir := expandTilde(dirPart)
	if listDir == "" {
		listDir = "."
	}
	entries, err := os.ReadDir(listDir)
	if err != nil {
		return nil
	}

	var items []tuist.Completion
	for _, entry := range entries {
		name := entry.Name()
		// Hide dotfiles unless the user has started typing one.
		if !strings.HasPrefix(base, ".") && strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasPrefix(name, base) {
			continue
		}
		isDir := entry.IsDir()
		display := name
		insert := "@" + dirPart + name
		kind := "file"
		if isDir {
			display += "/"
			insert += "/"
			kind = "dir"
		}
		items = append(items, tuist.Completion{
			Label:        insert,
			DisplayLabel: display,
			Detail:       kind,
			Kind:         kind,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Label < items[j].Label
	})
	return items
}

// expandTilde expands a leading "~" or "~/" to the user's home directory. Other
// paths (absolute or relative to the cwd) are returned unchanged.
func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// expandReferencePath resolves an @-path (the text after "@") to an absolute
// host path, expanding a leading "~" and resolving relative paths against the
// current working directory.
func expandReferencePath(p string) (string, error) {
	if p == "~" {
		return os.UserHomeDir()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	return filepath.Abs(p)
}

// referenceMountRel computes the reference-relative mount path for an absolute
// host path: relative to the home directory when the path is under it,
// otherwise just the basename.
func referenceMountRel(abs string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(abs)
}

// parseReferenceTokens extracts the @-path tokens from a prompt line. A token is
// a whitespace-delimited word starting with "@"; surrounding quotes/backticks
// and trailing sentence punctuation are stripped.
func parseReferenceTokens(line string) []string {
	var out []string
	for _, field := range strings.Fields(line) {
		rest, ok := strings.CutPrefix(field, "@")
		if !ok || rest == "" {
			continue
		}
		rest = strings.Trim(rest, "`'\"")
		rest = strings.TrimRight(rest, ".,;:!?)")
		if rest == "" {
			continue
		}
		out = append(out, rest)
	}
	return out
}

func (s *LLMSession) hasReference(mount string) bool {
	for _, r := range s.references {
		if r.mount == mount {
			return true
		}
	}
	return false
}

// attachReferences resolves any @-path tokens in input, mounting each into the
// LLM's workspace read-only under the references prefix (see
// core.WorkspaceReferencePrefix). Newly-attached references are sticky for the
// session and shown in the "References" sidebar. The returned string is the
// prompt annotated with the referenced paths' workspace locations so the model
// knows where to read them; nonexistent paths are skipped.
func (s *LLMSession) attachReferences(ctx context.Context, input string) string {
	_ = ctx
	tokens := parseReferenceTokens(input)
	if len(tokens) == 0 {
		return input
	}

	llm := s.llm
	changed := false
	seen := map[string]bool{}
	var mentioned []referenceInfo
	for _, tok := range tokens {
		abs, err := expandReferencePath(tok)
		if err != nil {
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil {
			// Skip paths that don't resolve to a real host file/directory.
			continue
		}
		rel := referenceMountRel(abs)
		mount := workspaceReferencePrefix + "/" + rel
		if seen[mount] {
			continue
		}
		seen[mount] = true
		info := referenceInfo{original: tok, mount: mount, isDir: fi.IsDir()}
		mentioned = append(mentioned, info)

		if s.hasReference(mount) {
			// Already attached earlier this session; just re-mention it.
			continue
		}
		ws := llm.Workspace()
		if fi.IsDir() {
			ws = ws.WithReferenceDirectory(rel, s.dag.Host().Directory(abs))
		} else {
			ws = ws.WithReferenceFile(rel, s.dag.Host().File(abs))
		}
		llm = llm.WithWorkspace(ws)
		s.references = append(s.references, info)
		changed = true
	}

	if changed {
		s.llm = llm
		s.updateReferencesPreview()
	}
	if len(mentioned) == 0 {
		return input
	}
	return input + referenceAnnotation(mentioned)
}

// referenceAnnotation renders the trailing block appended to a prompt that maps
// each referenced path to its read-only workspace location.
func referenceAnnotation(refs []referenceInfo) string {
	var b strings.Builder
	b.WriteString("\n\n[Referenced paths (read-only, available in your workspace):")
	for _, r := range refs {
		kind := ""
		if r.isDir {
			kind = " (directory)"
		}
		fmt.Fprintf(&b, "\n- %s%s → %s", r.original, kind, r.mount)
	}
	b.WriteString("\nRead them with your normal file tools at the workspace paths shown.]")
	return b.String()
}

// updateReferencesPreview refreshes the "References" sidebar section listing the
// host paths attached this session. An empty list clears the section.
func (s *LLMSession) updateReferencesPreview() {
	if len(s.references) == 0 {
		s.frontend.SetSidebarContent(idtui.SidebarSection{Title: "References"})
		return
	}
	refs := make([]referenceInfo, len(s.references))
	copy(refs, s.references)
	s.frontend.SetSidebarContent(idtui.SidebarSection{
		Title: "References",
		ContentFunc: func(width int) string {
			var buf strings.Builder
			out := idtui.NewOutput(&buf)
			for _, r := range refs {
				name := r.original
				if r.isDir && !strings.HasSuffix(name, "/") {
					name += "/"
				}
				fmt.Fprintln(&buf, out.String(name).Foreground(termenv.ANSICyan).String())
			}
			return strings.TrimRight(buf.String(), "\n")
		},
	})
}
