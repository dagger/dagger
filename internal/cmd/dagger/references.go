package daggercmd

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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

// tunnelInfo records a URL the user attached with @, along with the
// engine-side address it was remapped to. The address points at a
// container-to-host tunnel (Host.service): a service on the session network
// that forwards each connection through the CLI's own network, so the agent's
// containers can reach endpoints only the user's machine can see — localhost
// dev servers, VPN'd or intranet hosts. Forwarding is per connection and
// wholly lazy: the endpoint does not need to be listening when the reference
// is attached, only whenever the agent actually connects.
type tunnelInfo struct {
	original string // URL as typed after @, e.g. https://localhost:6060
	tunneled string // rewritten URL reachable from containers, e.g. https://<hostname>:6060
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

// parseReferenceURL reports whether an @-token is a URL reference rather than
// a host path. Only tokens with an explicit "scheme://" count — everything
// else keeps its host-path meaning — and the endpoint must resolve to a TCP
// port, either explicitly or implied by a well-known scheme, because the
// tunnel forwards raw TCP.
func parseReferenceURL(tok string) (u *url.URL, port int, ok bool) {
	if !strings.Contains(tok, "://") {
		return nil, 0, false
	}
	u, err := url.Parse(tok)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return nil, 0, false
	}
	port, ok = referenceURLPort(u)
	if !ok {
		return nil, 0, false
	}
	return u, port, true
}

// referenceURLPort resolves the TCP port of a URL reference: the explicit
// port when one was typed, or the scheme's well-known default. URLs with
// neither can't be tunneled, so they are not URL references.
func referenceURLPort(u *url.URL) (int, bool) {
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return 0, false
		}
		return n, true
	}
	switch u.Scheme {
	case "http", "ws":
		return 80, true
	case "https", "wss":
		return 443, true
	}
	return 0, false
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

// attachReferences resolves any @-path and @-URL tokens in input.
//
// Paths that already live inside the current workspace need no mounting: the
// agent can read them directly, so the token is simply rewritten in place to a
// path relative to the workspace's cwd (e.g. "@/abs/path/to/foo.go" →
// "./foo.go"). Paths outside the workspace are mounted into the LLM's
// workspace read-only under the references prefix (see
// workspaceReferencePrefix); those are sticky for the session and shown in the
// "References" sidebar, and the prompt is annotated with their workspace
// locations so the model knows where to read them. Nonexistent paths are left
// untouched.
//
// URLs (e.g. "@https://localhost:6060") name endpoints on the user's side of
// the network, so they get the container-to-host equivalent of a mount: a
// tunnel service on the session network forwarding to the endpoint via the
// host (see attachTunnel), with the token rewritten in place to the tunnel's
// engine-side address and the prompt annotated with the mapping. The upstream
// is dialed per connection, so a tunnel attaches even while nothing is
// listening yet; only URLs whose tunnel itself fails to start are left
// untouched.
func (a *sessionAgent) attachReferences(ctx context.Context, input string) string {
	tokens := parseReferenceTokens(input)
	if len(tokens) == 0 {
		return input
	}

	llm := a.llm
	changed := false
	tunneled := false
	seen := map[string]bool{}
	rewrites := map[string]string{}
	var mentioned []referenceInfo
	var mentionedTunnels []tunnelInfo
	for _, tok := range tokens {
		if u, port, ok := parseReferenceURL(tok); ok {
			if seen[tok] {
				continue
			}
			seen[tok] = true
			info, fresh, err := a.attachTunnel(ctx, tok, u, port)
			if err != nil {
				// Leave the token as typed: the model at least sees what the
				// user meant, even if it can't reach it.
				slog.Warn("failed to tunnel @-referenced URL", "url", tok, "error", err)
				continue
			}
			tunneled = tunneled || fresh
			rewrites[tok] = info.tunneled
			mentionedTunnels = append(mentionedTunnels, info)
			continue
		}

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
			rewrites[tok] = rel
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
	}
	if changed || tunneled {
		a.updateReferencesPreview()
	}
	if len(rewrites) > 0 {
		input = rewriteReferenceTokens(input, func(tok string) (string, bool) {
			r, ok := rewrites[tok]
			return r, ok
		})
	}
	if len(mentioned) > 0 {
		input += referenceAnnotation(mentioned)
	}
	if len(mentionedTunnels) > 0 {
		input += tunnelAnnotation(mentionedTunnels)
	}
	return input
}

// tunnelStartTimeout bounds how long a prompt submit blocks waiting for a
// container-to-host tunnel to come up. Starting one provisions a network
// namespace and health-checks the tunnel's listener (not the upstream: that
// is dialed per connection, so an endpoint nobody is listening on still
// attaches and simply refuses connections until it comes up). The timeout
// keeps a wedged engine from stalling the prompt indefinitely.
const tunnelStartTimeout = 10 * time.Second

// attachTunnel resolves an @-URL to its tunnelInfo, starting a
// container-to-host tunnel for it on first mention. Tunnels are sticky for
// the conversation like mounted references: a URL mentioned again reuses the
// address already handed out. fresh reports whether a new tunnel was
// attached.
func (a *sessionAgent) attachTunnel(ctx context.Context, tok string, u *url.URL, port int) (tunnelInfo, bool, error) {
	for _, t := range a.tunnels {
		if t.original == tok {
			return t, false, nil
		}
	}

	// Host.service proxies each connection through the CLI's own network, so
	// the upstream address resolves exactly as it does for the user:
	// localhost is the user's localhost. The frontend port mirrors the
	// backend, so the engine-side address keeps the port the user typed.
	svc := a.session.dag.Host().Service(
		[]dagger.PortForward{{
			Backend:  port,
			Frontend: port,
			Protocol: dagger.NetworkProtocolTcp,
		}},
		dagger.HostServiceOpts{Host: u.Hostname()},
	)
	ctx, cancel := context.WithTimeout(ctx, tunnelStartTimeout)
	defer cancel()
	// Start explicitly: nothing ever binds this service — containers reach it
	// by hostname on the session network — so it has to already be running.
	svc, err := svc.Start(ctx)
	if err != nil {
		return tunnelInfo{}, false, err
	}
	hostname, err := svc.Hostname(ctx)
	if err != nil {
		return tunnelInfo{}, false, err
	}

	info := tunnelInfo{original: tok, tunneled: tunnelURL(u, hostname, port)}
	a.tunnels = append(a.tunnels, info)
	return info, true, nil
}

// tunnelURL rewrites u to point at the tunnel's engine-side address, keeping
// everything but the authority's host and port.
func tunnelURL(u *url.URL, hostname string, port int) string {
	rewritten := *u
	rewritten.Host = net.JoinHostPort(hostname, strconv.Itoa(port))
	return rewritten.String()
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

// tunnelAnnotation renders the trailing block appended to a prompt that maps
// each @-referenced URL (rewritten in place) back to what the user typed, so
// the model knows the tunneled address stands in for the original endpoint.
func tunnelAnnotation(tunnels []tunnelInfo) string {
	var b strings.Builder
	b.WriteString("\n\n[Referenced URLs, remapped to tunnels reachable from your containers:")
	for _, t := range tunnels {
		fmt.Fprintf(&b, "\n- %s (as typed) → %s", t.original, t.tunneled)
	}
	b.WriteString("\nUse the remapped addresses; the originals only resolve on the user's machine. Connections are forwarded on demand, so a connection that is refused, reset, or closed without a response just means the endpoint is not up on the user's side right now — the same address works as soon as it is. TLS certificates, if any, are still issued for the original hostname.]")
	return b.String()
}

// updateReferencesPreview refreshes the "References" sidebar section listing
// the host paths and tunneled URLs attached to this conversation. An empty
// list clears the section. Like the other conversation-scoped surfaces it
// follows focus: a background conversation's references are not what the user
// is typing against.
func (a *sessionAgent) updateReferencesPreview() {
	if !a.uiActive() {
		return
	}
	if len(a.references) == 0 && len(a.tunnels) == 0 {
		a.session.frontend.SetSidebarContent(idtui.SidebarSection{Title: "References"})
		return
	}
	refs := make([]referenceInfo, len(a.references))
	copy(refs, a.references)
	tunnels := make([]tunnelInfo, len(a.tunnels))
	copy(tunnels, a.tunnels)
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
			for _, t := range tunnels {
				fmt.Fprintln(&buf, out.String(t.original).Foreground(termenv.ANSICyan).String())
			}
			return strings.TrimRight(buf.String(), "\n")
		},
	})
}
