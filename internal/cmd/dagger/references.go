package daggercmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// workspaceReferencePrefix is the workspace directory under which @-referenced
// host paths are mounted read-only (via Workspace.withMountedDirectory/
// withMountedFile). Purely a CLI convention: the engine treats it like any
// other mount path, so it only exists to give attached references a
// predictable, out-of-the-way home in the workspace.
const workspaceReferencePrefix = ".refs"

// referenceInfo records a host path the user attached with @, along with the
// read-only workspace path it was mounted at.
type referenceInfo struct {
	original string // path as typed after @, e.g. ~/foo/bar.txt
	mount    string // workspace-relative mount path, e.g. .refs/~/foo/bar.txt
	isDir    bool
}

// completeReferencePath completes an @-path against the host filesystem. frag
// is the text typed after the leading "@". A leading "~" is expanded to the
// home directory for listing, but the inserted text preserves the user's typed
// prefix (e.g. "~/") so the token round-trips.
func completeReferencePath(frag string) []tuist.Completion {
	dirPart, base := "", frag
	if idx := strings.LastIndex(frag, "/"); idx >= 0 {
		dirPart, base = frag[:idx+1], frag[idx+1:]
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
// host path, preserving the full path shape so that distinct host paths never
// collapse onto the same mount (e.g. /tmp/frontend/config.json vs
// /tmp/backend/config.json). Paths under the home directory are shortened to a
// "~/" prefix — mirroring how they're typed — and everything else keeps its
// path from the root, minus the leading separator.
func referenceMountRel(abs string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, abs); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(rel)
		}
	}
	rel := strings.TrimPrefix(abs, filepath.VolumeName(abs))
	return strings.TrimPrefix(filepath.ToSlash(rel), "/")
}

// parseReferenceTokens extracts the @-path tokens from a prompt line. A token is
// a whitespace-delimited word starting with "@"; surrounding quotes/backticks
// and trailing sentence punctuation are stripped.
func parseReferenceTokens(line string) []string {
	var out []string
	for _, field := range strings.Fields(line) {
		_, tok, _, ok := splitReferenceField(field)
		if !ok {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// referenceQuoteChars are stripped from both ends of an @-token, and
// referencePunctuation from its end, so a reference typed mid-sentence (e.g.
// `@foo.go,`) still resolves.
const (
	referenceQuoteChars  = "`'\""
	referencePunctuation = ".,;:!?)"
)

// splitReferenceField decomposes a whitespace-delimited word into the leading
// "@" plus any opening quotes, the path token itself, and the trailing
// quotes/punctuation. The three parts always concatenate back to field, so a
// token can be rewritten in place. ok is false when the word is not an
// @-reference.
func splitReferenceField(field string) (prefix, tok, suffix string, ok bool) {
	rest, ok := strings.CutPrefix(field, "@")
	if !ok || rest == "" {
		return "", "", "", false
	}
	lead := 0
	for lead < len(rest) && strings.IndexByte(referenceQuoteChars, rest[lead]) >= 0 {
		lead++
	}
	tail := len(rest)
	for tail > lead && strings.IndexByte(referenceQuoteChars, rest[tail-1]) >= 0 {
		tail--
	}
	body := rest[lead:tail]
	tok = strings.TrimRight(body, referencePunctuation)
	if tok == "" {
		return "", "", "", false
	}
	return "@" + rest[:lead], tok, body[len(tok):] + rest[tail:], true
}

// rewriteReferenceTokens replaces @-tokens in line for which replace returns a
// substitution, dropping the "@" sigil while preserving any quotes, trailing
// punctuation and the line's original whitespace.
func rewriteReferenceTokens(line string, replace func(tok string) (string, bool)) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		start := i
		for i < len(line) && isReferenceSpace(line[i]) {
			i++
		}
		b.WriteString(line[start:i])

		start = i
		for i < len(line) && !isReferenceSpace(line[i]) {
			i++
		}
		field := line[start:i]
		if prefix, tok, suffix, ok := splitReferenceField(field); ok {
			if replacement, ok := replace(tok); ok {
				b.WriteString(strings.TrimPrefix(prefix, "@") + replacement + suffix)
				continue
			}
		}
		b.WriteString(field)
	}
	return b.String()
}

func isReferenceSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func (a *sessionAgent) hasReference(mount string) bool {
	for _, r := range a.references {
		if r.mount == mount {
			return true
		}
	}
	return false
}

// attachReferences resolves any @-path tokens in input. Paths that already live
// inside the current workspace need no mounting: the agent can read them
// directly, so the token is simply rewritten in place to a path relative to the
// workspace's cwd (e.g. "@/abs/path/to/foo.go" → "./foo.go"). Paths outside the
// workspace are mounted into the LLM's workspace read-only under the references
// prefix (see workspaceReferencePrefix); those are sticky for the session
// and shown in the "References" sidebar, and the prompt is annotated with their
// workspace locations so the model knows where to read them. Nonexistent paths
// are left untouched.
func (a *sessionAgent) attachReferences(ctx context.Context, input string) string {
	tokens := parseReferenceTokens(input)
	if len(tokens) == 0 {
		return input
	}

	llm := a.llm
	changed := false
	seen := map[string]bool{}
	local := map[string]string{}
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

		// Already in the workspace: no need to copy it into .refs, just point
		// the model at it relative to the workspace cwd.
		if rel, ok := a.session.workspaceRelativePath(ctx, abs); ok {
			local[tok] = rel
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

		if a.hasReference(mount) {
			// Already attached earlier this session; just re-mention it.
			continue
		}
		ws := llm.Workspace()
		if fi.IsDir() {
			ws = ws.WithMountedDirectory(mount, a.session.dag.Host().Directory(abs))
		} else {
			ws = ws.WithMountedFile(mount, a.session.dag.Host().File(abs))
		}
		llm = llm.WithWorkspace(ws)
		a.references = append(a.references, info)
		changed = true
	}

	if changed {
		// Mounting references rebinds the workspace: a wholesale change to
		// the LLM value, so route it through updateLLM -- any live agent
		// (seeded with the old binding) is dropped, and the submit this call
		// is part of packages a fresh one from the new value.
		if err := a.updateLLM(llm); err != nil {
			slog.Warn("failed to refresh LLM after attaching references", "error", err)
		}
		a.updateReferencesPreview()
	}
	if len(local) > 0 {
		input = rewriteReferenceTokens(input, func(tok string) (string, bool) {
			rel, ok := local[tok]
			return rel, ok
		})
	}
	if len(mentioned) == 0 {
		return input
	}
	return input + referenceAnnotation(mentioned)
}

// workspaceRelativePath reports whether abs (an absolute host path) lies inside
// the current workspace, and if so returns it as a path relative to the
// workspace's cwd — the form the agent's own file tools take. Paths under the
// cwd are prefixed with "./" so they read unambiguously as paths; paths
// elsewhere in the workspace keep their "../" prefix.
func (s *LLMSession) workspaceRelativePath(ctx context.Context, abs string) (string, bool) {
	root, cwd, ok := s.workspaceHostPaths(ctx)
	if !ok {
		return "", false
	}
	abs = resolveSymlinks(abs)
	if rel, err := filepath.Rel(root, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// Outside the workspace boundary.
		return "", false
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return rel, true
	}
	return "./" + rel, true
}

// workspaceHostPaths resolves the host filesystem paths of the workspace root
// and its cwd, resolved once per session. Returns false for workspaces with no
// host location (remote or synthetic ones), where @-paths always need mounting.
func (s *LLMSession) workspaceHostPaths(ctx context.Context) (root, cwd string, ok bool) {
	if !s.workspaceHostResolved {
		s.workspaceHostResolved = true
		s.workspaceHostRoot, s.workspaceHostCwd = resolveWorkspaceHostPaths(ctx, s.dag)
	}
	if s.workspaceHostRoot == "" {
		return "", "", false
	}
	return s.workspaceHostRoot, s.workspaceHostCwd, true
}

// resolveWorkspaceHostPaths queries the current workspace for its address and
// cwd and maps them back to host paths. Both are empty when the workspace isn't
// local.
func resolveWorkspaceHostPaths(ctx context.Context, dag *dagger.Client) (root, cwd string) {
	ws := dag.CurrentWorkspace()
	address, err := ws.Address(ctx)
	if err != nil {
		return "", ""
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" {
		// Remote or synthetic workspace: nothing on the host to compare to.
		return "", ""
	}
	wsCwd, err := ws.Cwd(ctx)
	if err != nil {
		return "", ""
	}
	root, err = workspaceRootFromAddress(address, wsCwd)
	if err != nil {
		return "", ""
	}
	relCwd, err := workspaceRelativeCwd(wsCwd)
	if err != nil {
		return "", ""
	}
	root = resolveSymlinks(root)
	return root, filepath.Join(root, relCwd)
}

// resolveSymlinks resolves symlinks in p, falling back to the cleaned input when
// it can't (e.g. the path doesn't exist). Keeps host paths comparable when the
// workspace root and an @-path reach the same place through different links,
// such as macOS's /tmp → /private/tmp.
func resolveSymlinks(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
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
// host paths attached to this conversation. An empty list clears the section.
// Like the other conversation-scoped surfaces it follows focus: a background
// conversation's references are not what the user is typing against.
func (a *sessionAgent) updateReferencesPreview() {
	if !a.uiActive() {
		return
	}
	if len(a.references) == 0 {
		a.session.frontend.SetSidebarContent(idtui.SidebarSection{Title: "References"})
		return
	}
	refs := make([]referenceInfo, len(a.references))
	copy(refs, a.references)
	a.session.frontend.SetSidebarContent(idtui.SidebarSection{
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
