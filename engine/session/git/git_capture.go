package git

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
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
	"sync"
	"syscall"
	"unicode"
)

const captureGitFormatVersion = 2

const (
	captureHeadRef     = "refs/dagger/checkpoint/head"
	captureWorktreeRef = "refs/dagger/checkpoint/worktree"

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

var (
	captureApprovalKey     []byte
	captureApprovalKeyErr  error
	captureApprovalKeyOnce sync.Once
)

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
	return fmt.Sprintf("capture requires approval for %d selected dirty file(s)", len(e.candidates))
}

type captureArtifacts struct {
	metadata *CaptureGitMetadata
	bundle   string
	paths    []capturedPath
	remote   captureRemote
}

// CaptureGit performs all discovery, selection, and scanning in the client
// process. No repository or worktree bytes are sent until the bundle has been
// built, checked, and the checkout and selected files have been revalidated.
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

	unlock, err := gitCheckoutLocks.lock(ctx, filepath.Clean(req.GetCheckoutPath()))
	if err != nil {
		return err
	}
	defer unlock()

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
			metadata.ApprovalCandidates = approvalErr.candidates
		}
		return srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Metadata{Metadata: metadata}})
	}
	defer os.RemoveAll(filepath.Dir(artifacts.bundle))

	if err := srv.Send(&CaptureGitResponse{Msg: &CaptureGitResponse_Metadata{Metadata: artifacts.metadata}}); err != nil {
		return fmt.Errorf("send capture metadata: %w", err)
	}
	if err := streamCaptureFile(srv, artifacts.bundle, CAPTURE_CHUNK_BUNDLE); err != nil {
		return fmt.Errorf("send checkpoint bundle: %w", err)
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
		// A local-only checkpoint uses the session's canonical host repository
		// as its prerequisite, and transports only the approved worktree delta.
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		remote = captureRemote{baseSHA: state.headSHA}
	}
	approvals := stringSet(policy.GetApprovalTokens())
	committedBytes, err := scanCommittedObjects(ctx, checkout, remote.baseSHA, state.headSHA, limits)
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
	bundlePath := filepath.Join(tmpDir, "checkpoint.bundle")

	worktreeSHA, err := buildCaptureBundle(ctx, checkout, tmpDir, state.objectFormat, remote.baseSHA, state.headSHA, paths, bundlePath)
	if err != nil {
		return nil, err
	}
	if err := revalidateCapturedPaths(checkout, paths); err != nil {
		return nil, err
	}
	latest, err := collectCheckoutState(ctx, checkout)
	if err != nil || latest.digest() != state.digest() {
		return nil, errors.New("checkout changed during capture; retry")
	}
	if remote.sanitizedURL != "" {
		if err := revalidateCaptureRemote(ctx, checkout, remote); err != nil {
			return nil, err
		}
	}

	bundleDigest, bundleBytes, err := digestFile(bundlePath)
	if err != nil {
		return nil, errors.New("read checkpoint bundle failed")
	}
	metadata := &CaptureGitMetadata{
		FormatVersion:       captureGitFormatVersion,
		ObjectFormat:        state.objectFormat,
		RemoteUrl:           remote.sanitizedURL,
		RemoteRef:           remote.ref,
		BaseSha:             remote.baseSHA,
		HeadSha:             state.headSHA,
		CheckoutStateDigest: state.digest(),
		BundleSha256:        bundleDigest,
		BundleBytes:         bundleBytes,
		WorktreeSha:         worktreeSHA,
		TrackedFiles:        int32(trackedCount),
		UntrackedFiles:      int32(untrackedCount),
		SelectedBytes:       committedBytes + worktreeBytes,
	}
	cleanup = false
	return &captureArtifacts{metadata: metadata, bundle: bundlePath, paths: paths, remote: remote}, nil
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

// scanCommittedObjects measures the committed blobs the checkpoint bundle
// carries and holds them to the configured bounds.
//
// It deliberately does not apply the secret heuristics. This content is already
// recorded in Git history, so it is what the author chose to commit, and
// classifying it only asks about every revision of every ordinary source file
// whose text happens to resemble a token. The preflight exists for worktree
// state a checkpoint would otherwise carry off the host without it ever having
// been committed.
func scanCommittedObjects(ctx context.Context, checkout, base, head string, limits captureLimits) (int64, error) {
	out, err := runHostGit(ctx, checkout, "rev-list", "--objects", head, "^"+base)
	if err != nil {
		return 0, errors.New("enumerate prerequisite objects failed")
	}
	var objects bytes.Buffer
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
		objects.WriteString(oid)
		objects.WriteByte('\n')
	}
	if objects.Len() == 0 {
		return 0, nil
	}

	// Ask one long-lived cat-file process for every object's type and size.
	// Spawning separate `cat-file -t` and `cat-file -s` processes per object
	// makes capture time grow with both the object count and process startup
	// overhead, which is especially painful for a stack of local commits in a
	// large repository. Batch mode preserves Git's normal partial-clone behavior
	// while amortizing that work across the complete object set.
	objectsOut, err := runHostGitBytes(ctx, checkout, nil, &objects,
		"cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return 0, errors.New("inspect prerequisite objects failed")
	}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(string(objectsOut)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, errors.New("inspect prerequisite object failed")
		}
		if fields[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > limits.trackedFile || size > limits.total-total {
			return 0, errors.New("committed content exceeds the configured capture bounds")
		}
		total += size
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
		if matchesAnyCapturePattern(p, policy.GetExclude()) {
			continue
		}
		if len(policy.GetInclude()) > 0 && !matchesAnyCapturePattern(p, policy.GetInclude()) {
			continue
		}
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
		if len(policy.GetInclude()) > 0 && !matchesAnyCapturePattern(p, policy.GetInclude()) {
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
	var approvalCandidates []*CaptureGitCandidate
	missingApproval := false
	for _, candidate := range candidates {
		maxBytes := limits.untrackedFile
		if candidate.tracked {
			maxBytes = limits.trackedFile
		}
		cp, data, err := fingerprintCapturePath(checkout, candidate.path, candidate.tracked, maxBytes)
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
		classification := suspiciousCaptureClassification(candidate.path, data)
		if classification != "" {
			cp.suspicious = true
		}
		approvalToken, err := captureApprovalToken(cp, classification)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		approvalCandidates = append(approvalCandidates, &CaptureGitCandidate{
			Path: candidate.path, Classification: classification,
			Tracked: candidate.tracked, Bytes: cp.size, ApprovalToken: approvalToken,
		})
		_, retriedApproval := approvals[approvalToken]
		if !candidate.tracked && !retriedApproval && !matchesAnyCapturePattern(candidate.path, policy.GetInclude()) {
			missingApproval = true
		}
		selected = append(selected, cp)
	}
	if untrackedCount > limits.untrackedFiles || untrackedBytes > limits.untrackedTotal {
		return nil, 0, 0, 0, errors.New("untracked content exceeds the configured aggregate bounds")
	}
	if initialBytes+worktreeBytes > limits.total {
		return nil, 0, 0, 0, errors.New("selected content exceeds the configured capture bound")
	}
	if missingApproval {
		return nil, 0, 0, 0, &captureApprovalError{candidates: approvalCandidates}
	}
	return selected, trackedCount, untrackedCount, worktreeBytes, nil
}

func captureApprovalToken(path capturedPath, classification string) (string, error) {
	captureApprovalKeyOnce.Do(func() {
		captureApprovalKey = make([]byte, sha256.Size)
		_, captureApprovalKeyErr = rand.Read(captureApprovalKey)
	})
	if captureApprovalKeyErr != nil {
		return "", errors.New("initialize capture approval state failed")
	}
	mac := hmac.New(sha256.New, captureApprovalKey)
	fmt.Fprintf(mac, "%s\x00%t\x00%t\x00%d\x00%d\x00", path.path, path.tracked, path.deleted, path.mode, path.size)
	_, _ = mac.Write(path.digest[:])
	_, _ = mac.Write([]byte{'\x00'})
	_, _ = io.WriteString(mac, classification)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func fingerprintCapturePath(checkout, gitPath string, tracked bool, maxBytes int64) (capturedPath, []byte, error) {
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
		if tracked {
			if checkoutHasGitEntry(full) {
				return cp, nil, errors.New("capture rejected a changed submodule")
			}
			// A tracked file replaced by a directory is a deletion of the old
			// entry plus separately enumerated untracked files beneath it.
			cp.deleted = true
			return cp, nil, nil
		}
		return cp, nil, errors.New("capture rejected a selected directory boundary")
	}
	cp.mode = info.Mode()
	if info.Size() > maxBytes {
		return cp, nil, errors.New("worktree content exceeds the configured per-file bound")
	}
	var data []byte
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return cp, nil, errors.New("read selected symlink failed")
		}
		data = []byte(target)
	} else if info.Mode().IsRegular() {
		file, openErr := os.Open(full)
		if openErr != nil {
			return cp, nil, errors.New("open selected worktree content failed")
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
		if err != nil {
			return cp, nil, errors.New("read selected worktree content failed")
		}
	} else {
		return cp, nil, errors.New("capture rejected a socket, device, FIFO, or other special file")
	}
	cp.size = int64(len(data))
	if cp.size > maxBytes {
		return cp, nil, errors.New("worktree content exceeds the configured per-file bound")
	}
	cp.digest = sha256.Sum256(data)
	return cp, data, nil
}

func buildCaptureBundle(ctx context.Context, checkout, tmpDir, objectFormat, base, head string, paths []capturedPath, output string) (string, error) {
	if len(paths) == 0 && base == head {
		if err := os.WriteFile(output, nil, 0o600); err != nil {
			return "", errors.New("create empty checkpoint bundle failed")
		}
		return "", nil
	}

	stagingRepo := filepath.Join(tmpDir, "objects.git")
	if _, err := runHostGit(ctx, tmpDir, "init", "--bare", "--object-format="+objectFormat, stagingRepo); err != nil {
		return "", errors.New("initialize capture object database failed")
	}
	sourceObjectsOut, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return "", errors.New("locate checkout object database failed")
	}
	sourceObjects := strings.TrimSpace(sourceObjectsOut)
	alternatesPath := filepath.Join(stagingRepo, "objects", "info", "alternates")
	if err := os.WriteFile(alternatesPath, []byte(sourceObjects+"\n"), 0o600); err != nil {
		return "", errors.New("configure capture object database failed")
	}

	stageEnv := []string{
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(stagingRepo, "objects"),
		"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index"),
	}
	if _, err := runHostGitBytes(ctx, checkout, stageEnv, nil, "read-tree", head); err != nil {
		return "", errors.New("initialize worktree staging index failed")
	}
	if err := stageCapturePaths(ctx, checkout, stageEnv, paths); err != nil {
		return "", err
	}
	worktreeSHA := ""
	if len(paths) > 0 {
		var err error
		worktreeSHA, err = captureWorktreeCommit(ctx, checkout, stageEnv, head)
		if err != nil {
			return "", err
		}
	}

	if worktreeSHA == "" && base == head {
		if err := os.WriteFile(output, nil, 0o600); err != nil {
			return "", err
		}
		return "", nil
	}

	if _, err := runHostGit(ctx, stagingRepo, "update-ref", captureHeadRef, head); err != nil {
		return "", errors.New("advertise checkpoint head failed")
	}
	// Sparse pack traversal may add objects outside the exact revision closure
	// for copied trees, which the verification below intentionally rejects.
	bundleArgs := []string{"-c", "pack.useSparse=false", "bundle", "create", "--version=3", output, captureHeadRef}
	if worktreeSHA != "" {
		if _, err := runHostGit(ctx, stagingRepo, "update-ref", captureWorktreeRef, worktreeSHA); err != nil {
			return "", errors.New("advertise checkpoint worktree failed")
		}
		bundleArgs = append(bundleArgs, captureWorktreeRef)
	}
	bundleArgs = append(bundleArgs, "^"+base)
	if _, err := runHostGit(ctx, stagingRepo, bundleArgs...); err != nil {
		return "", errors.New("create checkpoint bundle failed")
	}
	if err := ensureCaptureHeadAdvertisement(output, base, head); err != nil {
		return "", err
	}
	if err := verifyCaptureBundle(ctx, stagingRepo, sourceObjects, tmpDir, output, objectFormat, base, head, worktreeSHA); err != nil {
		return "", err
	}
	return worktreeSHA, nil
}

type captureBundleHeader struct {
	version       string
	objectFormat  string
	prerequisites []string
	refs          map[string]string
}

func stageCapturePaths(ctx context.Context, checkout string, stageEnv []string, paths []capturedPath) error {
	var remove bytes.Buffer
	for _, p := range paths {
		if p.tracked {
			remove.WriteString(p.path)
			remove.WriteByte(0)
		}
	}
	if remove.Len() > 0 {
		if _, err := runHostGitBytes(ctx, checkout, stageEnv, &remove, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return errors.New("stage tracked worktree selection failed")
		}
	}
	for _, expected := range paths {
		if expected.deleted {
			continue
		}
		got, data, err := fingerprintCapturePath(checkout, expected.path, expected.tracked, expected.size)
		if err != nil || got.deleted != expected.deleted || got.mode != expected.mode || got.size != expected.size || got.digest != expected.digest {
			return errors.New("selected worktree content changed during capture; retry")
		}
		blobOut, err := runHostGitBytes(ctx, checkout, stageEnv, bytes.NewReader(data),
			"hash-object", "--no-filters", "-w", "--stdin")
		if err != nil {
			return errors.New("write selected worktree content failed")
		}
		mode := "100644"
		switch {
		case got.mode&os.ModeSymlink != 0:
			mode = "120000"
		case got.mode.Perm()&0o111 != 0:
			mode = "100755"
		}
		if _, err := runHostGitBytes(ctx, checkout, stageEnv, nil,
			"update-index", "--add", "--replace", "--cacheinfo", mode, strings.TrimSpace(string(blobOut)), expected.path); err != nil {
			return errors.New("stage selected worktree content failed")
		}
	}

	return nil
}

func captureWorktreeCommit(ctx context.Context, checkout string, stageEnv []string, head string) (string, error) {
	treeOut, err := runHostGitBytes(ctx, checkout, stageEnv, nil, "write-tree")
	if err != nil {
		return "", errors.New("write selected worktree tree failed")
	}
	headTree, err := runHostGit(ctx, checkout, "rev-parse", head+"^{tree}")
	if err != nil {
		return "", errors.New("read captured HEAD tree failed")
	}
	tree := strings.TrimSpace(string(treeOut))
	if tree == strings.TrimSpace(headTree) {
		return "", nil
	}
	commitEnv := append([]string{
		"GIT_AUTHOR_NAME=Dagger Workspace Capture",
		"GIT_AUTHOR_EMAIL=workspace-capture@dagger.invalid",
		"GIT_AUTHOR_DATE=1970-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Dagger Workspace Capture",
		"GIT_COMMITTER_EMAIL=workspace-capture@dagger.invalid",
		"GIT_COMMITTER_DATE=1970-01-01T00:00:00Z",
	}, stageEnv...)
	commitOut, err := runHostGitBytes(ctx, checkout, commitEnv, strings.NewReader("Dagger workspace snapshot\n"),
		"commit-tree", tree, "-p", head)
	if err != nil {
		return "", errors.New("create synthetic worktree commit failed")
	}
	return strings.TrimSpace(string(commitOut)), nil
}

func parseCaptureBundleHeader(data []byte) (captureBundleHeader, error) {
	header := captureBundleHeader{refs: map[string]string{}}
	end := bytes.Index(data, []byte("\n\n"))
	if end < 0 || end > 1<<20 {
		return header, errors.New("checkpoint bundle has an invalid header")
	}
	lines := strings.Split(string(data[:end]), "\n")
	if len(lines) == 0 {
		return header, errors.New("checkpoint bundle header is empty")
	}
	header.version = lines[0]
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "@object-format="):
			if len(fields) != 1 || header.objectFormat != "" {
				return header, errors.New("checkpoint bundle has invalid object-format capabilities")
			}
			header.objectFormat = strings.TrimPrefix(line, "@object-format=")
		case strings.HasPrefix(line, "@"):
			return header, fmt.Errorf("checkpoint bundle has unsupported capability %q", line)
		case strings.HasPrefix(line, "-"):
			if len(fields) == 0 || len(fields[0]) < 2 {
				return header, errors.New("checkpoint bundle has an invalid prerequisite")
			}
			header.prerequisites = append(header.prerequisites, strings.TrimPrefix(fields[0], "-"))
		default:
			if len(fields) != 2 {
				return header, errors.New("checkpoint bundle has an invalid advertised ref")
			}
			if _, exists := header.refs[fields[1]]; exists {
				return header, fmt.Errorf("checkpoint bundle advertises ref %s more than once", fields[1])
			}
			header.refs[fields[1]] = fields[0]
		}
	}
	return header, nil
}

// Git omits a positive ref when its object is also the prerequisite. A dirty
// L == R capture still needs to advertise both design refs, so add the
// self-verifying head line to the standard header and immediately ask stock Git
// to verify and unbundle the result below.
func ensureCaptureHeadAdvertisement(bundlePath, base, head string) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return errors.New("read checkpoint bundle header failed")
	}
	header, err := parseCaptureBundleHeader(data)
	if err != nil {
		return err
	}
	if header.refs[captureHeadRef] == head {
		return nil
	}
	if head != base || header.refs[captureHeadRef] != "" {
		return errors.New("checkpoint bundle did not advertise the logical head")
	}
	end := bytes.Index(data, []byte("\n\n"))
	patched := make([]byte, 0, len(data)+len(head)+len(captureHeadRef)+2)
	patched = append(patched, data[:end]...)
	patched = append(patched, '\n')
	patched = append(patched, head...)
	patched = append(patched, ' ')
	patched = append(patched, captureHeadRef...)
	patched = append(patched, data[end:]...)
	if err := os.WriteFile(bundlePath, patched, 0o600); err != nil {
		return errors.New("write checkpoint bundle header failed")
	}
	return nil
}

func verifyCaptureBundle(ctx context.Context, stagingRepo, sourceObjects, tmpDir, bundlePath, objectFormat, base, head, worktreeSHA string) error {
	if _, err := runHostGit(ctx, stagingRepo, "bundle", "verify", bundlePath); err != nil {
		return errors.New("verify checkpoint bundle failed")
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return errors.New("read checkpoint bundle failed")
	}
	header, err := parseCaptureBundleHeader(data)
	if err != nil {
		return err
	}
	if header.version != "# v3 git bundle" || header.objectFormat != objectFormat {
		return errors.New("checkpoint bundle version or object format is invalid")
	}
	if len(header.prerequisites) != 1 || header.prerequisites[0] != base {
		return errors.New("checkpoint bundle prerequisite is invalid")
	}
	expectedRefs := map[string]string{captureHeadRef: head}
	if worktreeSHA != "" {
		expectedRefs[captureWorktreeRef] = worktreeSHA
	}
	if len(header.refs) != len(expectedRefs) {
		return errors.New("checkpoint bundle advertised an unexpected ref set")
	}
	for ref, oid := range expectedRefs {
		if header.refs[ref] != oid {
			return fmt.Errorf("checkpoint bundle ref %s does not resolve to %s", ref, oid)
		}
	}

	tip := head
	if worktreeSHA != "" {
		tip = worktreeSHA
	}
	expected, err := captureObjectSet(ctx, stagingRepo, nil, "rev-list", "--objects", tip, "^"+base)
	if err != nil {
		return errors.New("enumerate selected checkpoint object closure failed")
	}
	verifyRepo := filepath.Join(tmpDir, "verify.git")
	if _, err := runHostGit(ctx, tmpDir, "init", "--bare", "--object-format="+objectFormat, verifyRepo); err != nil {
		return errors.New("initialize bundle verification database failed")
	}
	packOffset := bytes.Index(data, []byte("\n\n")) + 2
	alternateEnv := []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + sourceObjects}
	// A prerequisite bundle contains a thin pack. `bundle unbundle` repairs that
	// pack by appending prerequisite objects used as delta bases, so enumerating
	// its resulting pack would count objects that were not actually transmitted.
	// Index the repaired pack, then use its offsets and the original pack's
	// object count to distinguish incoming entries from appended delta bases.
	actual, err := captureIncomingPackObjectSet(ctx, verifyRepo, alternateEnv, data[packOffset:], objectFormat)
	if err != nil {
		return errors.New("enumerate checkpoint bundle objects failed")
	}
	if !equalStringSets(expected, actual) {
		return fmt.Errorf("checkpoint bundle object closure differs from selected closure (%d expected, %d packed)", len(expected), len(actual))
	}
	return nil
}

type capturedPackObject struct {
	oid    string
	offset uint64
}

func captureIncomingPackObjectSet(ctx context.Context, repo string, env []string, pack []byte, objectFormat string) (map[string]struct{}, error) {
	if len(pack) < 12 || string(pack[:4]) != "PACK" {
		return nil, errors.New("checkpoint bundle pack header is invalid")
	}
	incomingCount := int(binary.BigEndian.Uint32(pack[8:12]))
	if _, err := runHostGitBytes(ctx, repo, env, bytes.NewReader(pack), "index-pack", "--stdin", "--fix-thin"); err != nil {
		return nil, fmt.Errorf("index checkpoint bundle pack: %w", err)
	}
	indexes, err := filepath.Glob(filepath.Join(repo, "objects", "pack", "*.idx"))
	if err != nil {
		return nil, fmt.Errorf("locate checkpoint bundle pack index: %w", err)
	}
	if len(indexes) != 1 {
		return nil, fmt.Errorf("checkpoint bundle produced %d pack indexes", len(indexes))
	}
	out, err := runHostGitBytes(ctx, repo, nil, nil, "verify-pack", "-v", indexes[0])
	if err != nil {
		return nil, fmt.Errorf("inspect checkpoint bundle pack: %w", err)
	}
	oidLen := 40
	if objectFormat == "sha256" {
		oidLen = 64
	}
	objects := make([]capturedPackObject, 0, incomingCount)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || len(fields[0]) != oidLen {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		offset, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse checkpoint bundle pack offset: %w", err)
		}
		objects = append(objects, capturedPackObject{oid: fields[0], offset: offset})
	}
	if len(objects) < incomingCount {
		return nil, fmt.Errorf("checkpoint bundle pack contains %d objects, expected at least %d", len(objects), incomingCount)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].offset < objects[j].offset })
	actual := make(map[string]struct{}, incomingCount)
	for _, object := range objects[:incomingCount] {
		actual[object.oid] = struct{}{}
	}
	if len(actual) != incomingCount {
		return nil, errors.New("checkpoint bundle pack contains duplicate objects")
	}
	return actual, nil
}

func captureObjectSet(ctx context.Context, repo string, env []string, args ...string) (map[string]struct{}, error) {
	out, err := runHostGitBytes(ctx, repo, env, nil, args...)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			set[fields[0]] = struct{}{}
		}
	}
	return set, nil
}

func equalStringSets(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for value := range a {
		if _, ok := b[value]; !ok {
			return false
		}
	}
	return true
}

func revalidateCapturedPaths(checkout string, paths []capturedPath) error {
	for _, expected := range paths {
		got, _, err := fingerprintCapturePath(checkout, expected.path, expected.tracked, expected.size)
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
