package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

const captureGitFormatVersion = 1

const (
	defaultMaxUntrackedFileBytes  = 16 << 20
	defaultMaxUntrackedTotalBytes = 64 << 20
	defaultMaxUntrackedFiles      = 4096
	defaultMaxTrackedFileBytes    = 128 << 20
	defaultMaxCaptureBytes        = 512 << 20
	suspiciousBinaryBytes         = 1 << 20
)

type captureLimits struct {
	untrackedFile, untrackedTotal, trackedFile, total int64
	untrackedFiles                                    int
}

type captureRemote struct {
	name, sanitizedURL, ref, advertisedSHA, baseSHA string
	distance                                        int
}

// captureAdvertisedRef is one ref a remote currently advertises, reduced to
// what base selection needs. sha is the advertised object, which revalidation
// compares against a later advertisement, and commit is that object peeled to a
// commit, which is what history queries take.
type captureAdvertisedRef struct {
	remote, url, ref, sha, commit string
}

// captureLocalOnlyEnv keeps base selection's object lookups inside the local
// store. Selection probes commits a remote advertises but the checkout may not
// have, and in a partial clone an ordinary lookup silently fetches every miss
// from the promisor remote, which is both a network round trip per advertised
// ref and a quiet write to the checkout being captured.
var captureLocalOnlyEnv = []string{"GIT_NO_LAZY_FETCH=1"}

type capturedPath struct {
	path       string
	tracked    bool
	deleted    bool
	mode       os.FileMode
	size       int64
	digest     [sha256.Size]byte
	suspicious bool
}

type captureApprovalError struct {
	candidates []*CaptureGitCandidate
}

func (e *captureApprovalError) Error() string {
	return fmt.Sprintf("capture rejected %d suspicious file(s) without exact path approval", len(e.candidates))
}

type captureArtifacts struct {
	metadata *CaptureGitMetadata
	bundle   string
	patch    string
	paths    []capturedPath
	remote   captureRemote
}

// CaptureGit performs all discovery, selection, and scanning in the client
// process. No repository or worktree bytes are sent until both payloads have
// been built, checked, and the checkout and selected files have been
// revalidated.
func (s GitAttachable) CaptureGit(req *CaptureGitRequest, srv Git_CaptureGitServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()

	sendErr := func(errorType ErrorInfo_ErrorType, message string) error {
		return srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Metadata{Metadata: &CaptureGitMetadata{
			FormatVersion: captureGitFormatVersion,
			Error:         &ErrorInfo{Type: errorType, Message: message},
		}}})
	}
	if req.GetCheckoutPath() == "" {
		return sendErr(INVALID_REQUEST, "checkout path is required")
	}
	if req.GetPolicy() == nil {
		return sendErr(INVALID_REQUEST, "an explicit capture policy is required")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return sendErr(NOT_FOUND, gitMissingMessage)
	}
	if !checkoutHasGitEntry(req.GetCheckoutPath()) {
		return sendErr(NOT_A_REPO, "capture requires a Git checkout at its root")
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	artifacts, err := captureGitArtifacts(ctx, req.GetCheckoutPath(), req.GetPolicy())
	if err != nil {
		// Candidate bytes never cross this boundary on a rejected preflight.
		// Structured path metadata is returned only so the schema/CLI can prompt
		// locally and retry with exact approvals.
		metadata := &CaptureGitMetadata{
			FormatVersion: captureGitFormatVersion,
			Error:         &ErrorInfo{Type: CAPTURE_FAILED, Message: err.Error()},
		}
		var approvalErr *captureApprovalError
		if errors.As(err, &approvalErr) {
			metadata.Error.Type = CAPTURE_REJECTED
			metadata.SuspiciousCandidates = approvalErr.candidates
		}
		return srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Metadata{Metadata: metadata}})
	}
	defer os.RemoveAll(filepath.Dir(artifacts.bundle))

	if err := srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Metadata{Metadata: artifacts.metadata}}); err != nil {
		return fmt.Errorf("send capture metadata: %w", err)
	}
	if err := streamCaptureFile(srv, artifacts.bundle, CAPTURE_CHUNK_PREREQUISITE_BUNDLE); err != nil {
		return fmt.Errorf("send prerequisite bundle: %w", err)
	}
	if err := streamCaptureFile(srv, artifacts.patch, CAPTURE_CHUNK_WORKTREE_DELTA); err != nil {
		return fmt.Errorf("send worktree delta: %w", err)
	}
	return nil
}

func captureGitArtifacts(ctx context.Context, checkout string, policy *CaptureGitPolicy) (*captureArtifacts, error) {
	limits, err := normalizeCaptureLimits(policy)
	if err != nil {
		return nil, err
	}
	state, err := collectCheckoutState(ctx, checkout)
	if err != nil || state.headSHA == "" {
		return nil, errors.New("capture requires an existing Git HEAD")
	}

	remote, err := selectCaptureRemote(ctx, checkout, state.headSHA)
	if err != nil {
		return nil, err
	}
	commits, err := captureCommitMetadata(ctx, checkout, remote.baseSHA, state.headSHA)
	if err != nil {
		return nil, err
	}
	approvals := stringSet(policy.GetApproveSuspicious())
	committedBytes, err := scanCommittedObjects(ctx, checkout, remote.baseSHA, state.headSHA, approvals, limits)
	if err != nil {
		return nil, err
	}
	paths, trackedCount, untrackedCount, worktreeBytes, err := selectAndScanWorktree(ctx, checkout, state.headSHA, policy, approvals, limits, committedBytes)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "dagger-capture-git")
	if err != nil {
		return nil, errors.New("create capture staging area failed")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	bundlePath := filepath.Join(tmpDir, "prerequisite.bundle")
	patchPath := filepath.Join(tmpDir, "worktree.patch")

	if remote.baseSHA == state.headSHA {
		// There are no local committed objects beyond the remote prerequisite.
		// An empty bundle payload represents that state; restore skips import.
		if err := os.WriteFile(bundlePath, nil, 0o600); err != nil {
			return nil, errors.New("create empty prerequisite bundle failed")
		}
	} else {
		if _, err := runHostGit(ctx, checkout, "bundle", "create", bundlePath, "HEAD", "^"+remote.baseSHA); err != nil {
			return nil, errors.New("create prerequisite bundle failed")
		}
		if _, err := runHostGit(ctx, checkout, "bundle", "verify", bundlePath); err != nil {
			return nil, errors.New("verify prerequisite bundle failed")
		}
	}
	if err := buildSelectedWorktreePatch(ctx, checkout, state.headSHA, paths, patchPath); err != nil {
		return nil, err
	}
	if err := revalidateCapturedPaths(checkout, paths); err != nil {
		return nil, err
	}
	latest, err := collectCheckoutState(ctx, checkout)
	if err != nil || latest.headSHA != state.headSHA {
		return nil, errors.New("checkout changed during capture; retry")
	}
	if err := revalidateCaptureRemote(ctx, checkout, remote); err != nil {
		return nil, err
	}

	bundleDigest, bundleBytes, err := digestFile(bundlePath)
	if err != nil {
		return nil, errors.New("read prerequisite bundle failed")
	}
	patchDigest, patchBytes, err := digestFile(patchPath)
	if err != nil {
		return nil, errors.New("read worktree delta failed")
	}
	metadata := &CaptureGitMetadata{
		FormatVersion:  captureGitFormatVersion,
		ObjectFormat:   state.objectFormat,
		RemoteUrl:      remote.sanitizedURL,
		RemoteRef:      remote.ref,
		BaseSha:        remote.baseSHA,
		HeadSha:        state.headSHA,
		Commits:        commits,
		BundleSha256:   bundleDigest,
		BundleBytes:    bundleBytes,
		WorktreeSha256: patchDigest,
		WorktreeBytes:  patchBytes,
		TrackedFiles:   int32(trackedCount),
		UntrackedFiles: int32(untrackedCount),
		SelectedBytes:  committedBytes + worktreeBytes,
	}
	cleanup = false
	return &captureArtifacts{metadata: metadata, bundle: bundlePath, patch: patchPath, paths: paths, remote: remote}, nil
}

func normalizeCaptureLimits(policy *CaptureGitPolicy) (captureLimits, error) {
	limits := captureLimits{
		untrackedFile: policy.GetMaxUntrackedFileBytes(), untrackedTotal: policy.GetMaxUntrackedTotalBytes(),
		untrackedFiles: int(policy.GetMaxUntrackedFiles()), trackedFile: policy.GetMaxTrackedFileBytes(), total: policy.GetMaxTotalBytes(),
	}
	if limits.untrackedFile == 0 {
		limits.untrackedFile = defaultMaxUntrackedFileBytes
	}
	if limits.untrackedTotal == 0 {
		limits.untrackedTotal = defaultMaxUntrackedTotalBytes
	}
	if limits.untrackedFiles == 0 {
		limits.untrackedFiles = defaultMaxUntrackedFiles
	}
	if limits.trackedFile == 0 {
		limits.trackedFile = defaultMaxTrackedFileBytes
	}
	if limits.total == 0 {
		limits.total = defaultMaxCaptureBytes
	}
	if limits.untrackedFile < 0 || limits.untrackedTotal < 0 || limits.untrackedFiles < 0 || limits.trackedFile < 0 || limits.total < 0 {
		return limits, errors.New("capture limits must not be negative")
	}
	return limits, nil
}

// selectCaptureRemote proves the closest ancestor of head that a remote still
// advertises, and names the ref a restore fetches to get it.
//
// The work is bounded by the number of remotes, not by how many refs they
// advertise. One listing per remote is unavoidable, but everything after it
// answers for the whole candidate set at once: a single batched object probe,
// a single history walk, and a halving search for the ref that carries the
// base. A repository whose remotes advertise tens of thousands of refs costs
// the same handful of local git invocations as one with a dozen.
func selectCaptureRemote(ctx context.Context, checkout, head string) (captureRemote, error) {
	unproven := errors.New("no currently advertised remote-backed ancestor was found")

	advertised := advertisedCaptureRefs(ctx, checkout)
	if len(advertised) == 0 {
		return captureRemote{}, unproven
	}
	commits := make([]string, len(advertised))
	for i, ref := range advertised {
		commits[i] = ref.commit
	}
	// Walking head against every candidate at once yields the best base any
	// single one of them can prove, which is the base to look for a ref for.
	nearest, _, ok := captureFrontier(ctx, checkout, head, commits)
	if !ok {
		return captureRemote{}, unproven
	}
	selected, ok := advertisedRefReaching(ctx, checkout, nearest, advertised)
	if !ok {
		return captureRemote{}, unproven
	}
	// Measure against the one ref being recorded rather than against the
	// combined candidate set, so the recorded base is exactly what a restore
	// fetching that ref finds.
	base, distance, ok := captureFrontier(ctx, checkout, head, []string{selected.commit})
	if !ok {
		return captureRemote{}, unproven
	}
	return captureRemote{
		name:          selected.remote,
		sanitizedURL:  sanitizeRemoteURL(selected.url),
		ref:           selected.ref,
		advertisedSHA: selected.sha,
		baseSHA:       base,
		distance:      distance,
	}, nil
}

// advertisedCaptureRefs lists what every configured remote currently
// advertises, in remote preference order, keeping only the refs that can
// actually prove a base: ones a restore can fetch by name, and whose commit the
// checkout already has.
func advertisedCaptureRefs(ctx context.Context, checkout string) []captureAdvertisedRef {
	var refs []captureAdvertisedRef
	seen := map[string]struct{}{}
	for _, remote := range orderedCaptureRemotes(ctx, checkout) {
		out, err := runHostGit(ctx, checkout, "ls-remote", "--refs", remote)
		if err != nil {
			continue
		}
		urlOut, err := runHostGit(ctx, checkout, "remote", "get-url", remote)
		if err != nil {
			continue
		}
		url := strings.TrimSpace(urlOut)
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			sha, ref := fields[0], fields[1]
			// Branches and tags are the refs a restore can name. Forge-managed
			// namespaces such as refs/pull/* and mirrored refs/remotes/* are not
			// something a checkout is based on, and on a large repository they
			// are the bulk of the advertisement.
			if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
				continue
			}
			// Remotes share history, so the same commit is usually advertised
			// many times over. Keeping the first sighting keeps the preferred
			// remote's name for it.
			if _, ok := seen[sha]; ok {
				continue
			}
			seen[sha] = struct{}{}
			refs = append(refs, captureAdvertisedRef{remote: remote, url: url, ref: ref, sha: sha})
		}
	}
	return localAdvertisedCommits(ctx, checkout, refs)
}

// localAdvertisedCommits keeps the advertised refs whose commit the checkout
// already has, resolving annotated tags to the commit they name. One batched
// probe answers for every ref at once.
//
// A ref the checkout has no commit for cannot prove anything: a base has to be
// an ancestor of head, so it is already local, and what selection needs from a
// ref is the history that connects the two. Fetching the rest would be a
// network round trip per advertised ref to learn about commits that cannot
// improve the answer.
func localAdvertisedCommits(ctx context.Context, checkout string, refs []captureAdvertisedRef) []captureAdvertisedRef {
	if len(refs) == 0 {
		return nil
	}
	var stdin bytes.Buffer
	for _, ref := range refs {
		stdin.WriteString(ref.sha)
		stdin.WriteString("^{commit}\n")
	}
	out, err := runHostGitBytes(ctx, checkout, captureLocalOnlyEnv, &stdin, "cat-file", "--batch-check")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(refs) {
		return nil
	}
	local := make([]captureAdvertisedRef, 0, len(refs))
	for i, line := range lines {
		// A resolved object reports "<oid> commit <size>"; a missing one echoes
		// the request followed by "missing".
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "commit" {
			continue
		}
		resolved := refs[i]
		resolved.commit = fields[0]
		local = append(local, resolved)
	}
	return local
}

// captureFrontier walks head's history against a set of commits at once and
// reports the newest ancestor of head those commits already contain, along with
// how many commits sit between it and head. That ancestor is the base a bundle
// can be cut from, and the count is what makes one candidate better than
// another.
func captureFrontier(ctx context.Context, checkout, head string, commits []string) (string, int, bool) {
	var stdin bytes.Buffer
	stdin.WriteString(head)
	stdin.WriteString("\n")
	for _, commit := range commits {
		stdin.WriteString("^")
		stdin.WriteString(commit)
		stdin.WriteString("\n")
	}
	out, err := runHostGitBytes(ctx, checkout, captureLocalOnlyEnv, &stdin,
		"rev-list", "--topo-order", "--boundary", "--stdin")
	if err != nil {
		return "", 0, false
	}
	var base string
	distance := 0
	for _, line := range strings.Fields(string(out)) {
		if boundary, ok := strings.CutPrefix(line, "-"); ok {
			if base == "" {
				base = boundary
			}
			continue
		}
		distance++
	}
	if distance == 0 {
		// The walk emitted nothing because head itself is advertised, so it is
		// already its own base.
		return head, 0, true
	}
	// Reaching a root without meeting a boundary means these commits share no
	// history with head and cannot serve as a base.
	return base, distance, base != ""
}

// advertisedRefReaching names the ref to record for a chosen base. A restore
// fetches the recorded ref by name and expects the base in its history, so the
// ref has to actually contain it.
//
// One reachability query answers for a whole set of refs at once, so this
// halves the candidates rather than testing them one by one. Candidates stay in
// remote preference order and the lower half is tried first, so the most
// preferred ref that carries the base wins.
func advertisedRefReaching(ctx context.Context, checkout, base string, refs []captureAdvertisedRef) (captureAdvertisedRef, bool) {
	if len(refs) == 0 || !captureReaches(ctx, checkout, base, refs) {
		return captureAdvertisedRef{}, false
	}
	for len(refs) > 1 {
		half := len(refs) / 2
		if captureReaches(ctx, checkout, base, refs[:half]) {
			refs = refs[:half]
		} else {
			refs = refs[half:]
		}
	}
	return refs[0], true
}

// captureReaches reports whether any of these refs already contains base.
// Asking for base while excluding every candidate lists nothing exactly when
// one of them contains it.
func captureReaches(ctx context.Context, checkout, base string, refs []captureAdvertisedRef) bool {
	var stdin bytes.Buffer
	stdin.WriteString(base)
	stdin.WriteString("\n")
	for _, ref := range refs {
		stdin.WriteString("^")
		stdin.WriteString(ref.commit)
		stdin.WriteString("\n")
	}
	out, err := runHostGitBytes(ctx, checkout, captureLocalOnlyEnv, &stdin,
		"rev-list", "--max-count=1", "--stdin")
	return err == nil && len(bytes.TrimSpace(out)) == 0
}

func orderedCaptureRemotes(ctx context.Context, checkout string) []string {
	var ordered []string
	if branchOut, err := runHostGit(ctx, checkout, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		if upstream, err := runHostGit(ctx, checkout, "config", "--get", "branch."+strings.TrimSpace(branchOut)+".remote"); err == nil {
			ordered = append(ordered, strings.TrimSpace(upstream))
		}
	}
	if pushDefault, err := runHostGit(ctx, checkout, "config", "--get", "remote.pushDefault"); err == nil {
		ordered = append(ordered, strings.TrimSpace(pushDefault))
	}
	ordered = append(ordered, "origin")
	if out, err := runHostGit(ctx, checkout, "remote"); err == nil {
		ordered = append(ordered, strings.Fields(out)...)
	}
	seen := map[string]struct{}{}
	result := ordered[:0]
	for _, name := range ordered {
		if name == "" || name == "." {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if _, err := runHostGit(ctx, checkout, "remote", "get-url", name); err == nil {
			result = append(result, name)
		}
	}
	return result
}

func sanitizeRemoteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	if strings.EqualFold(u.Scheme, "ssh") && u.User != nil {
		// SSH usernames select the remote account and are not credentials. Keep
		// the username, but never retain a URL password.
		u.User = url.User(u.User.Username())
	} else {
		u.User = nil
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func revalidateCaptureRemote(ctx context.Context, checkout string, remote captureRemote) error {
	out, err := runHostGit(ctx, checkout, "ls-remote", "--refs", remote.name, remote.ref)
	if err != nil {
		return errors.New("remote advertisement changed during capture; retry")
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == remote.advertisedSHA && fields[1] == remote.ref {
			return nil
		}
	}
	return errors.New("remote advertisement changed during capture; retry")
}

func captureCommitMetadata(ctx context.Context, checkout, base, head string) ([]*CaptureGitCommit, error) {
	out, err := runHostGit(ctx, checkout, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, errors.New("enumerate local history failed")
	}
	shas := strings.Fields(out)
	commits := make([]*CaptureGitCommit, 0, len(shas))
	expectedParent := base
	for _, sha := range shas {
		parentsOut, err := runHostGit(ctx, checkout, "rev-list", "--parents", "-n", "1", sha)
		if err != nil {
			return nil, errors.New("inspect local history failed")
		}
		parents := strings.Fields(parentsOut)
		if len(parents) != 2 || parents[1] != expectedParent {
			return nil, errors.New("portable capture requires linear local history without merges")
		}
		format := "%H%x00%B%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI"
		metaOut, err := runHostGit(ctx, checkout, "show", "-s", "--format="+format, sha)
		if err != nil {
			return nil, errors.New("read local commit metadata failed")
		}
		fields := strings.SplitN(strings.TrimSuffix(metaOut, "\n"), "\x00", 8)
		if len(fields) != 8 {
			return nil, errors.New("invalid local commit metadata")
		}
		pathsOut, err := runHostGitBytes(ctx, checkout, nil, nil, "diff-tree", "--no-commit-id", "--name-only", "-z", "-r", sha+"^!")
		if err != nil {
			return nil, errors.New("read local commit paths failed")
		}
		message := strings.TrimSuffix(fields[1], "\n")
		commits = append(commits, &CaptureGitCommit{Sha: fields[0], Message: message, AuthorName: fields[2], AuthorEmail: fields[3], AuthorDate: fields[4], CommitterName: fields[5], CommitterEmail: fields[6], CommitterDate: fields[7], Paths: splitNullPaths(pathsOut)})
		expectedParent = sha
	}
	if expectedParent != head {
		return nil, errors.New("local history does not connect to HEAD")
	}
	return commits, nil
}

func scanCommittedObjects(ctx context.Context, checkout, base, head string, approvals map[string]struct{}, limits captureLimits) (int64, error) {
	out, err := runHostGit(ctx, checkout, "rev-list", "--objects", head, "^"+base)
	if err != nil {
		return 0, errors.New("enumerate prerequisite objects failed")
	}
	var total int64
	var suspicious []*CaptureGitCandidate
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.SplitN(line, " ", 2)
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		oid := fields[0]
		if _, ok := seen[oid]; ok {
			continue
		}
		seen[oid] = struct{}{}
		typeOut, err := runHostGit(ctx, checkout, "cat-file", "-t", oid)
		if err != nil || strings.TrimSpace(typeOut) != "blob" {
			continue
		}
		sizeOut, err := runHostGit(ctx, checkout, "cat-file", "-s", oid)
		if err != nil {
			return 0, errors.New("inspect prerequisite blob failed")
		}
		size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
		if err != nil || size > limits.trackedFile || total+size > limits.total {
			return 0, errors.New("committed content exceeds the configured capture bounds")
		}
		var p string
		if len(fields) == 2 {
			p = fields[1]
		}
		blob, err := runHostGitBytes(ctx, checkout, nil, nil, "cat-file", "blob", oid)
		if err != nil {
			return 0, errors.New("read prerequisite blob failed")
		}
		if classification := suspiciousCaptureClassification(p, blob); classification != "" {
			if _, ok := approvals[p]; !ok {
				suspicious = append(suspicious, &CaptureGitCandidate{Path: p, Classification: classification, Tracked: true, Bytes: size})
			}
		}
		total += size
	}
	if len(suspicious) > 0 {
		return 0, &captureApprovalError{candidates: suspicious}
	}
	return total, nil
}

func selectAndScanWorktree(ctx context.Context, checkout, head string, policy *CaptureGitPolicy, approvals map[string]struct{}, limits captureLimits, initialBytes int64) ([]capturedPath, int, int, int64, error) {
	trackedOut, err := runHostGitBytes(ctx, checkout, nil, nil, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", head, "--")
	if err != nil {
		return nil, 0, 0, 0, errors.New("enumerate tracked worktree changes failed")
	}
	untrackedOut, err := runHostGitBytes(ctx, checkout, nil, nil, "ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return nil, 0, 0, 0, errors.New("enumerate untracked worktree files failed")
	}
	for _, p := range splitNullPaths(untrackedOut) {
		if strings.HasSuffix(p, "/") {
			return nil, 0, 0, 0, errors.New("capture rejected an untracked nested repository")
		}
	}

	var candidates []struct {
		path    string
		tracked bool
	}
	for _, p := range splitNullPaths(trackedOut) {
		if !matchesAnyCapturePattern(p, policy.GetExclude()) {
			candidates = append(candidates, struct {
				path    string
				tracked bool
			}{p, true})
		}
	}
	for _, p := range splitNullPaths(untrackedOut) {
		if matchesAnyCapturePattern(p, policy.GetExclude()) {
			continue
		}
		includes := policy.GetIncludeUntracked()
		if len(includes) > 0 && !matchesAnyCapturePattern(p, includes) {
			continue
		}
		candidates = append(candidates, struct {
			path    string
			tracked bool
		}{p, false})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })

	var selected []capturedPath
	var worktreeBytes, untrackedBytes int64
	trackedCount, untrackedCount := 0, 0
	var suspicious []*CaptureGitCandidate
	for _, candidate := range candidates {
		cp, data, err := fingerprintCapturePath(checkout, candidate.path, candidate.tracked)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		if candidate.tracked {
			trackedCount++
			if cp.size > limits.trackedFile {
				return nil, 0, 0, 0, errors.New("tracked worktree content exceeds the configured per-file bound")
			}
		} else {
			untrackedCount++
			untrackedBytes += cp.size
			if cp.size > limits.untrackedFile {
				return nil, 0, 0, 0, errors.New("untracked content exceeds the configured per-file bound")
			}
		}
		worktreeBytes += cp.size
		if classification := suspiciousCaptureClassification(candidate.path, data); classification != "" {
			cp.suspicious = true
			if _, ok := approvals[candidate.path]; !ok {
				suspicious = append(suspicious, &CaptureGitCandidate{Path: candidate.path, Classification: classification, Tracked: candidate.tracked, Bytes: cp.size})
			}
		}
		selected = append(selected, cp)
	}
	if untrackedCount > limits.untrackedFiles || untrackedBytes > limits.untrackedTotal {
		return nil, 0, 0, 0, errors.New("untracked content exceeds the configured aggregate bounds")
	}
	if initialBytes+worktreeBytes > limits.total {
		return nil, 0, 0, 0, errors.New("selected content exceeds the configured capture bound")
	}
	if len(suspicious) > 0 {
		return nil, 0, 0, 0, &captureApprovalError{candidates: suspicious}
	}
	return selected, trackedCount, untrackedCount, worktreeBytes, nil
}

func fingerprintCapturePath(checkout, gitPath string, tracked bool) (capturedPath, []byte, error) {
	cp := capturedPath{path: gitPath, tracked: tracked}
	full := filepath.Join(checkout, filepath.FromSlash(gitPath))
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		if tracked {
			cp.deleted = true
			return cp, nil, nil
		}
		return cp, nil, errors.New("selected untracked file disappeared during preflight")
	}
	if err != nil {
		return cp, nil, errors.New("inspect selected worktree content failed")
	}
	if info.IsDir() {
		if tracked && checkoutHasGitEntry(full) {
			return cp, nil, errors.New("capture rejected a changed submodule")
		}
		return cp, nil, errors.New("capture rejected a selected directory boundary")
	}
	cp.mode = info.Mode()
	var data []byte
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return cp, nil, errors.New("read selected symlink failed")
		}
		data = []byte(target)
	} else if info.Mode().IsRegular() {
		data, err = os.ReadFile(full)
		if err != nil {
			return cp, nil, errors.New("read selected worktree content failed")
		}
	} else {
		return cp, nil, errors.New("capture rejected a socket, device, FIFO, or other special file")
	}
	cp.size = int64(len(data))
	cp.digest = sha256.Sum256(data)
	return cp, data, nil
}

func buildSelectedWorktreePatch(ctx context.Context, checkout, head string, paths []capturedPath, output string) error {
	tmpDir, err := os.MkdirTemp("", "dagger-capture-index")
	if err != nil {
		return errors.New("create worktree staging index failed")
	}
	defer os.RemoveAll(tmpDir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index")}
	if _, err := runHostGitBytes(ctx, checkout, env, nil, "read-tree", head); err != nil {
		return errors.New("initialize worktree staging index failed")
	}
	var remove, add bytes.Buffer
	for _, p := range paths {
		if p.tracked {
			remove.WriteString(p.path)
			remove.WriteByte(0)
		}
		if !p.deleted {
			add.WriteString(p.path)
			add.WriteByte(0)
		}
	}
	if remove.Len() > 0 {
		if _, err := runHostGitBytes(ctx, checkout, env, &remove, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return errors.New("stage tracked worktree selection failed")
		}
	}
	if add.Len() > 0 {
		if _, err := runHostGitBytes(ctx, checkout, env, &add, "update-index", "--add", "--replace", "-z", "--stdin"); err != nil {
			return errors.New("stage selected worktree content failed")
		}
	}
	patch, err := runHostGitBytes(ctx, checkout, env, nil, "diff", "--cached", "--binary", "--full-index", "--no-renames", "--no-ext-diff", head, "--")
	if err != nil {
		return errors.New("create selected worktree delta failed")
	}
	if err := os.WriteFile(output, patch, 0o600); err != nil {
		return errors.New("write selected worktree delta failed")
	}
	return nil
}

func revalidateCapturedPaths(checkout string, paths []capturedPath) error {
	for _, expected := range paths {
		got, _, err := fingerprintCapturePath(checkout, expected.path, expected.tracked)
		if err != nil || got.deleted != expected.deleted || got.mode != expected.mode || got.size != expected.size || got.digest != expected.digest {
			return errors.New("selected worktree content changed during capture; retry")
		}
	}
	return nil
}

func suspiciousCaptureClassification(name string, data []byte) string {
	lower := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(lower)
	credentialNames := []string{".env", ".npmrc", ".pypirc", "id_rsa", "id_ed25519", "credentials", "credentials.json", "service-account.json", "token", "secrets.yml", "secrets.yaml"}
	for _, candidate := range credentialNames {
		if base == candidate {
			return "credential-path"
		}
	}
	credentialDirs := []string{"/.aws/", "/.ssh/", "/.gnupg/", "/.kube/", "/.config/gcloud/"}
	padded := "/" + lower
	for _, dir := range credentialDirs {
		if strings.Contains(padded, dir) {
			return "credential-path"
		}
	}
	upper := bytes.ToUpper(data)
	markers := [][]byte{[]byte("-----BEGIN PRIVATE KEY-----"), []byte("-----BEGIN RSA PRIVATE KEY-----"), []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), []byte("AWS_SECRET_ACCESS_KEY"), []byte("GITHUB_TOKEN="), []byte("GH_TOKEN="), []byte("NPM_TOKEN=")}
	for _, marker := range markers {
		if bytes.Contains(upper, marker) {
			return "credential-content"
		}
	}
	if len(data) >= suspiciousBinaryBytes && bytes.IndexByte(data, 0) >= 0 {
		return "large-binary"
	}
	if highEntropyToken(data) {
		return "high-entropy-content"
	}
	return ""
}

func highEntropyToken(data []byte) bool {
	const minToken = 40
	run, classes := 0, uint8(0)
	for _, b := range data {
		var class uint8
		switch {
		case b >= 'a' && b <= 'z':
			class = 1
		case b >= 'A' && b <= 'Z':
			class = 2
		case b >= '0' && b <= '9':
			class = 4
		case strings.ContainsRune("_-/+=", rune(b)):
			class = 8
		default:
			run, classes = 0, 0
			continue
		}
		run++
		classes |= class
		if run >= minToken && classes&(classes-1) != 0 && unicode.IsPrint(rune(b)) {
			return true
		}
	}
	return false
}

func matchesAnyCapturePattern(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
		if strings.HasSuffix(pattern, "/**") && strings.HasPrefix(name, strings.TrimSuffix(pattern, "**")) {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func digestFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func streamCaptureFile(srv Git_CaptureGitServer, name string, kind CaptureGitChunk_Kind) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if err := srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Chunk{Chunk: &CaptureGitChunk{Kind: kind, Data: data}}}); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}
