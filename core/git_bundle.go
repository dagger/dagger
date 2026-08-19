package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Git object format, not a security primitive
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vektah/gqlparser/v2/ast"
)

// Git bundle ingestion is intended for ordinary source-history transport, not
// adversarial archives. These bounds cheaply prevent accidental runaway work
// before Git is asked to inspect or import the bundle.
const (
	MaxGitBundleBytes   = int64(engineutil.MaxFileContentsSize)
	MaxGitBundleRefs    = 1024
	MaxGitBundleObjects = 1_000_000

	gitBundleCommandTimeout = 10 * time.Minute
	maxGitBundleHeaderBytes = 4 << 20
	maxGitBundleHeaderLine  = 64 << 10
)

// GitBundle is a standard Git bundle and its lazily parsed header.
type GitBundle struct {
	File             dagql.ObjectResult[*File]
	Version          int             `field:"true" doc:"Bundle format version (2 or 3)."`
	ObjectFormat     string          `field:"true" name:"objectFormat" doc:"Object format capability: sha1 or sha256."`
	Refs             []*GitBundleRef `field:"true" doc:"Refs advertised by the bundle and the object IDs they resolve to."`
	PrerequisiteSHAs []string        `field:"true" name:"prerequisiteSHAs" doc:"Commits that must already exist wherever this bundle is applied."`
}

// GitBundleRef is a ref advertised by a Git bundle.
type GitBundleRef struct {
	Name string `field:"true" doc:"The advertised ref name."`
	SHA  string `field:"true" name:"sha" doc:"The object ID the advertised ref resolves to."`
}

var (
	_ dagql.PersistedObject        = (*GitBundle)(nil)
	_ dagql.PersistedObjectDecoder = (*GitBundle)(nil)
	_ dagql.HasDependencyResults   = (*GitBundle)(nil)
)

func (*GitBundle) Type() *ast.Type {
	return &ast.Type{NamedType: "GitBundle", NonNull: true}
}

func (*GitBundle) TypeDescription() string {
	return "A Git bundle: a self-describing container of refs and the objects needed to reconstruct them, optionally rooted at prerequisite commits."
}

func (*GitBundleRef) Type() *ast.Type {
	return &ast.Type{NamedType: "GitBundleRef", NonNull: true}
}

func (*GitBundleRef) TypeDescription() string {
	return "A ref advertised by a Git bundle."
}

func (bundle *GitBundle) Clone() *GitBundle {
	if bundle == nil {
		return nil
	}
	cp := *bundle
	cp.Refs = make([]*GitBundleRef, len(bundle.Refs))
	for i, ref := range bundle.Refs {
		if ref == nil {
			continue
		}
		refCopy := *ref
		cp.Refs[i] = &refCopy
	}
	cp.PrerequisiteSHAs = slices.Clone(bundle.PrerequisiteSHAs)
	return &cp
}

func (bundle *GitBundle) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if bundle == nil || bundle.File.Self() == nil {
		return nil, nil
	}
	attached, err := attach(bundle.File)
	if err != nil {
		return nil, fmt.Errorf("attach git bundle file: %w", err)
	}
	file, ok := attached.(dagql.ObjectResult[*File])
	if !ok {
		return nil, fmt.Errorf("attach git bundle file: unexpected result %T", attached)
	}
	bundle.File = file
	return []dagql.AnyResult{file}, nil
}

type persistedGitBundlePayload struct {
	FileResultID     uint64          `json:"fileResultID"`
	Version          int             `json:"version"`
	ObjectFormat     string          `json:"objectFormat"`
	Refs             []*GitBundleRef `json:"refs"`
	PrerequisiteSHAs []string        `json:"prerequisiteSHAs,omitempty"`
}

func (bundle *GitBundle) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if bundle == nil || bundle.File.Self() == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git bundle: missing bundle file")
	}
	fileID, err := encodePersistedObjectRef(cache, bundle.File, "git bundle file")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload, err := json.Marshal(persistedGitBundlePayload{
		FileResultID:     fileID,
		Version:          bundle.Version,
		ObjectFormat:     bundle.ObjectFormat,
		Refs:             bundle.Clone().Refs,
		PrerequisiteSHAs: slices.Clone(bundle.PrerequisiteSHAs),
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted git bundle: %w", err)
	}
	return encodePersistedObjectRawJSON(payload), nil
}

func (*GitBundle) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitBundlePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git bundle: %w", err)
	}
	if persisted.FileResultID == 0 {
		return nil, fmt.Errorf("decode persisted git bundle: missing bundle file")
	}
	file, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.FileResultID, "git bundle file")
	if err != nil {
		return nil, err
	}
	return &GitBundle{
		File:             file,
		Version:          persisted.Version,
		ObjectFormat:     persisted.ObjectFormat,
		Refs:             persisted.Refs,
		PrerequisiteSHAs: slices.Clone(persisted.PrerequisiteSHAs),
	}, nil
}

// ParseGitBundle lazily reads and validates only a bundle's textual header. It
// deliberately does not walk or import the pack; ValidateGitBundle performs
// that more expensive work.
func ParseGitBundle(ctx context.Context, file dagql.ObjectResult[*File]) (*GitBundle, error) {
	if file.Self() == nil {
		return nil, fmt.Errorf("git bundle file is required")
	}
	if err := validateGitBundleFileSize(ctx, file); err != nil {
		return nil, err
	}
	reader, err := file.Self().Open(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("open git bundle: %w", err)
	}
	defer reader.Close()

	header, _, err := parseGitBundleHeader(reader)
	if err != nil {
		return nil, err
	}
	header.File = file
	return header, nil
}

func validateGitBundleFileSize(ctx context.Context, file dagql.ObjectResult[*File]) error {
	stat, err := file.Self().Stat(ctx, file)
	if err != nil {
		return fmt.Errorf("stat git bundle: %w", err)
	}
	if stat.Size <= 0 {
		return fmt.Errorf("git bundle file is empty")
	}
	if int64(stat.Size) > MaxGitBundleBytes {
		return fmt.Errorf("git bundle size %d exceeds limit %d", stat.Size, MaxGitBundleBytes)
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

func parseGitBundleHeader(input io.Reader) (*GitBundle, int64, error) {
	counted := &countingReader{r: input}
	reader := bufio.NewReaderSize(counted, maxGitBundleHeaderLine)
	readLine := func() (string, error) {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", fmt.Errorf("git bundle header line exceeds limit %d", maxGitBundleHeaderLine)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", fmt.Errorf("git bundle header is truncated")
			}
			return "", fmt.Errorf("read git bundle header: %w", err)
		}
		if counted.n-int64(reader.Buffered()) > maxGitBundleHeaderBytes {
			return "", fmt.Errorf("git bundle header exceeds limit %d", maxGitBundleHeaderBytes)
		}
		return strings.TrimSuffix(string(line), "\n"), nil
	}

	signature, err := readLine()
	if err != nil {
		return nil, 0, err
	}
	bundle := &GitBundle{ObjectFormat: "sha1"}
	switch signature {
	case "# v2 git bundle":
		bundle.Version = 2
	case "# v3 git bundle":
		bundle.Version = 3
	default:
		return nil, 0, fmt.Errorf("invalid git bundle signature %q", signature)
	}

	seenEntries := false
	seenObjectFormat := false
	seenRefs := map[string]struct{}{}
	for {
		line, err := readLine()
		if err != nil {
			return nil, 0, err
		}
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "@") {
			if bundle.Version != 3 || seenEntries {
				return nil, 0, fmt.Errorf("invalid git bundle capability placement")
			}
			capability, value, ok := strings.Cut(strings.TrimPrefix(line, "@"), "=")
			if capability == "object-format" {
				if !ok || seenObjectFormat {
					return nil, 0, fmt.Errorf("invalid git bundle object-format capability")
				}
				bundle.ObjectFormat = value
				seenObjectFormat = true
			}
			continue
		}
		seenEntries = true

		prerequisite := strings.HasPrefix(line, "-")
		entry := strings.TrimPrefix(line, "-")
		sha, name, ok := strings.Cut(entry, " ")
		if !ok || sha == "" || name == "" {
			return nil, 0, fmt.Errorf("invalid git bundle header entry %q", line)
		}
		if err := validateGitObjectID(bundle.ObjectFormat, sha); err != nil {
			return nil, 0, fmt.Errorf("invalid git bundle header entry %q: %w", line, err)
		}
		if prerequisite {
			bundle.PrerequisiteSHAs = append(bundle.PrerequisiteSHAs, sha)
		} else {
			if _, exists := seenRefs[name]; exists {
				return nil, 0, fmt.Errorf("git bundle advertises duplicate ref %q", name)
			}
			seenRefs[name] = struct{}{}
			bundle.Refs = append(bundle.Refs, &GitBundleRef{Name: name, SHA: sha})
		}
		if len(bundle.Refs)+len(bundle.PrerequisiteSHAs) > MaxGitBundleRefs {
			return nil, 0, fmt.Errorf("git bundle header contains more than %d refs and prerequisites", MaxGitBundleRefs)
		}
	}
	if bundle.ObjectFormat != "sha1" && bundle.ObjectFormat != "sha256" {
		return nil, 0, fmt.Errorf("unsupported git bundle object format %q", bundle.ObjectFormat)
	}
	if len(bundle.Refs) == 0 {
		return nil, 0, fmt.Errorf("git bundle advertises no refs")
	}
	return bundle, counted.n - int64(reader.Buffered()), nil
}

func validateGitObjectID(objectFormat, value string) error {
	want := sha1.Size
	if objectFormat == "sha256" {
		want = sha256.Size
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != want {
		return fmt.Errorf("%q is not a %s object ID", value, objectFormat)
	}
	return nil
}

// ValidateGitBundle performs bounded structural verification. For bundles
// without prerequisites it additionally imports the advertised refs into a
// fresh repository, letting Git verify complete object connectivity. A
// prerequisite bundle's final connectivity check necessarily happens in
// ImportGitBundle, after the required objects have been fetched.
func ValidateGitBundle(ctx context.Context, bundle *GitBundle) error {
	if bundle == nil || bundle.File.Self() == nil {
		return fmt.Errorf("git bundle is required")
	}
	if err := validateGitBundleFileSize(ctx, bundle.File); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, gitBundleCommandTimeout)
	defer cancel()
	return bundle.File.Self().Mount(ctx, bundle.File, func(path string) error {
		header, err := inspectGitBundleFile(path)
		if err != nil {
			return err
		}
		if !gitBundleHeadersEqual(bundle, header) {
			return fmt.Errorf("git bundle header changed after parsing")
		}
		if len(header.PrerequisiteSHAs) != 0 {
			return nil
		}

		tmp, err := os.MkdirTemp("", "dagger-git-bundle-validate-")
		if err != nil {
			return fmt.Errorf("create git bundle validation repository: %w", err)
		}
		defer os.RemoveAll(tmp)
		if _, err := runGitEnv(ctx, tmp, nil, "init", "--bare", "--quiet", "--object-format="+header.ObjectFormat); err != nil {
			return fmt.Errorf("initialize git bundle validation repository: %w", err)
		}
		if err := verifyGitBundleInRepo(ctx, tmp, path); err != nil {
			return fmt.Errorf("verify git bundle: %w", err)
		}
		if err := fetchGitBundleRefs(ctx, tmp, path, header.Refs); err != nil {
			return fmt.Errorf("verify git bundle connectivity: %w", err)
		}
		return nil
	})
}

func gitBundleHeadersEqual(a, b *GitBundle) bool {
	if a == nil || b == nil || a.Version != b.Version || a.ObjectFormat != b.ObjectFormat || !slices.Equal(a.PrerequisiteSHAs, b.PrerequisiteSHAs) || len(a.Refs) != len(b.Refs) {
		return false
	}
	for i := range a.Refs {
		if a.Refs[i] == nil || b.Refs[i] == nil || *a.Refs[i] != *b.Refs[i] {
			return false
		}
	}
	return true
}

func inspectGitBundleFile(path string) (*GitBundle, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open git bundle: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat git bundle: %w", err)
	}
	if stat.Size() <= 0 {
		return nil, fmt.Errorf("git bundle file is empty")
	}
	if stat.Size() > MaxGitBundleBytes {
		return nil, fmt.Errorf("git bundle size %d exceeds limit %d", stat.Size(), MaxGitBundleBytes)
	}

	header, packOffset, err := parseGitBundleHeader(file)
	if err != nil {
		return nil, err
	}
	hashSize := sha1.Size
	var packHash hash.Hash = sha1.New() //nolint:gosec // Git pack checksum
	if header.ObjectFormat == "sha256" {
		hashSize = sha256.Size
		packHash = sha256.New()
	}
	packSize := stat.Size() - packOffset
	if packSize < int64(12+hashSize) {
		return nil, fmt.Errorf("git bundle pack is truncated")
	}
	if _, err := file.Seek(packOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek git bundle pack: %w", err)
	}
	var packHeader [12]byte
	if _, err := io.ReadFull(file, packHeader[:]); err != nil {
		return nil, fmt.Errorf("read git bundle pack header: %w", err)
	}
	if string(packHeader[:4]) != "PACK" {
		return nil, fmt.Errorf("invalid git bundle pack signature")
	}
	packVersion := binary.BigEndian.Uint32(packHeader[4:8])
	if packVersion != 2 && packVersion != 3 {
		return nil, fmt.Errorf("unsupported git bundle pack version %d", packVersion)
	}
	objectCount := binary.BigEndian.Uint32(packHeader[8:12])
	if objectCount > MaxGitBundleObjects {
		return nil, fmt.Errorf("git bundle object count %d exceeds limit %d", objectCount, MaxGitBundleObjects)
	}
	if _, err := file.Seek(packOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek git bundle pack: %w", err)
	}
	if _, err := io.CopyN(packHash, file, packSize-int64(hashSize)); err != nil {
		return nil, fmt.Errorf("hash git bundle pack: %w", err)
	}
	trailer := make([]byte, hashSize)
	if _, err := io.ReadFull(file, trailer); err != nil {
		return nil, fmt.Errorf("read git bundle pack checksum: %w", err)
	}
	if !bytes.Equal(packHash.Sum(nil), trailer) {
		return nil, fmt.Errorf("git bundle pack checksum does not match content")
	}
	return header, nil
}

// CreateGitBundleFile creates a version-3 bundle from named refs in the
// repository's canonical object database. The temporary refs and repository
// live only in a scratch snapshot; the source repository is never mutated.
func CreateGitBundleFile(ctx context.Context, repo *GitRepository, refs []string, base *GitRef) (_ *File, rerr error) {
	if repo == nil || repo.Backend == nil {
		return nil, fmt.Errorf("git repository is required")
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("git bundle requires at least one ref")
	}
	if len(refs) > MaxGitBundleRefs {
		return nil, fmt.Errorf("git bundle ref count %d exceeds limit %d", len(refs), MaxGitBundleRefs)
	}

	targets := make([]*gitutil.Ref, 0, len(refs))
	backends := make([]GitRefBackend, 0, len(refs)+1)
	seen := map[string]struct{}{}
	for _, name := range refs {
		if name == "" || strings.HasPrefix(name, "-") {
			return nil, fmt.Errorf("invalid git bundle ref %q", name)
		}
		ref, err := repo.Remote.Lookup(name)
		if err != nil {
			return nil, fmt.Errorf("resolve git bundle ref %q: %w", name, err)
		}
		if ref.Name == "" {
			return nil, fmt.Errorf("git bundle ref %q does not resolve to a named ref", name)
		}
		if _, exists := seen[ref.Name]; exists {
			return nil, fmt.Errorf("git bundle ref %q resolves to duplicate ref %q", name, ref.Name)
		}
		seen[ref.Name] = struct{}{}
		backend, err := repo.Backend.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ref)
		backends = append(backends, backend)
	}
	if base != nil {
		if base.Ref == nil || base.Ref.SHA == "" {
			return nil, fmt.Errorf("git bundle base is missing its commit SHA")
		}
		backends = append(backends, base.Backend)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bkref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git bundle"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, gitBundleCommandTimeout)
	defer cancel()
	err = repo.Backend.mount(ctx, 0, false, backends, func(source *gitutil.GitCLI) error {
		sourceURL, err := source.URL(ctx)
		if err != nil {
			return fmt.Errorf("locate canonical git repository: %w", err)
		}
		objectFormatOut, err := source.Run(ctx, "rev-parse", "--show-object-format")
		if err != nil {
			return fmt.Errorf("read git repository object format: %w", err)
		}
		objectFormat := strings.TrimSpace(string(objectFormatOut))
		if objectFormat != "sha1" && objectFormat != "sha256" {
			return fmt.Errorf("unsupported git repository object format %q", objectFormat)
		}

		return MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
			scratch := filepath.Join(root, ".git-bundle-source")
			if err := os.MkdirAll(scratch, 0o700); err != nil {
				return err
			}
			if _, err := runGitEnv(ctx, scratch, nil, "init", "--bare", "--quiet", "--object-format="+objectFormat); err != nil {
				return fmt.Errorf("initialize git bundle repository: %w", err)
			}
			for _, target := range targets {
				if _, err := runGitEnv(ctx, scratch, nil, "fetch", "--quiet", "--no-tags", sourceURL, target.SHA+":"+target.Name); err != nil {
					return fmt.Errorf("fetch git bundle ref %s: %w", target.Name, err)
				}
			}

			bundleArgs := []string{"bundle", "create", "--version=3", filepath.Join(root, "repository.bundle")}
			for _, target := range targets {
				bundleArgs = append(bundleArgs, target.Name)
			}
			if base != nil {
				baseRef := "refs/dagger/bundle/base"
				if _, err := runGitEnv(ctx, scratch, nil, "fetch", "--quiet", "--no-tags", sourceURL, base.Ref.SHA+":"+baseRef); err != nil {
					return fmt.Errorf("fetch git bundle base %s: %w", base.Ref.SHA, err)
				}
				bundleArgs = append(bundleArgs, "^"+baseRef)
			}
			if _, err := runGitEnv(ctx, scratch, nil, bundleArgs...); err != nil {
				return fmt.Errorf("create git bundle: %w", err)
			}
			if err := os.RemoveAll(scratch); err != nil {
				return fmt.Errorf("remove git bundle scratch repository: %w", err)
			}

			header, err := inspectGitBundleFile(filepath.Join(root, "repository.bundle"))
			if err != nil {
				return fmt.Errorf("validate created git bundle: %w", err)
			}
			if base != nil && !slices.Contains(header.PrerequisiteSHAs, base.Ref.SHA) {
				return fmt.Errorf("git bundle base %s is not reachable from the bundled refs", base.Ref.SHA)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	snapshot, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	file := &File{
		Platform: query.Platform(),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	file.File.setValue("/repository.bundle")
	file.Snapshot.setValue(snapshot)
	return file, nil
}

// ImportGitBundle materializes a canonical bare repository containing the
// bundle's refs and every exact prerequisite fetched from repo. The source
// canonical repository is read-only; all imported refs live in the returned
// immutable snapshot.
func ImportGitBundle(ctx context.Context, repo *GitRepository, bundle *GitBundle, prerequisiteRef string) (_ *Directory, rerr error) {
	if repo == nil || repo.Backend == nil {
		return nil, fmt.Errorf("git repository is required")
	}
	if bundle == nil || bundle.File.Self() == nil {
		return nil, fmt.Errorf("git bundle is required")
	}
	if err := validateGitBundleFileSize(ctx, bundle.File); err != nil {
		return nil, err
	}

	prerequisites := make([]*gitutil.Ref, len(bundle.PrerequisiteSHAs))
	for i, sha := range bundle.PrerequisiteSHAs {
		prerequisites[i] = &gitutil.Ref{SHA: sha}
	}
	if prerequisiteRef != "" && len(prerequisites) > 0 {
		hint, err := repo.Remote.Lookup(prerequisiteRef)
		if err != nil {
			return nil, fmt.Errorf("resolve git bundle prerequisite ref %q: %w", prerequisiteRef, err)
		}
		matched := false
		for i, prerequisite := range prerequisites {
			if prerequisite.SHA == hint.SHA {
				prerequisites[i] = &gitutil.Ref{Name: hint.Name, SHA: prerequisite.SHA}
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("git bundle prerequisite ref %q resolves to %s, which is not a bundle prerequisite", prerequisiteRef, hint.SHA)
		}
	}
	backends := make([]GitRefBackend, 0, len(prerequisites))
	for _, prerequisite := range prerequisites {
		backend, err := repo.Backend.Get(ctx, prerequisite)
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bkref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("git bundle repository"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, gitBundleCommandTimeout)
	defer cancel()
	err = bundle.File.Self().Mount(ctx, bundle.File, func(bundlePath string) error {
		header, err := inspectGitBundleFile(bundlePath)
		if err != nil {
			return err
		}
		if !gitBundleHeadersEqual(bundle, header) {
			return fmt.Errorf("git bundle header changed after parsing")
		}

		mountErr := repo.Backend.mount(ctx, 0, false, backends, func(source *gitutil.GitCLI) error {
			sourceURL, err := source.URL(ctx)
			if err != nil {
				return fmt.Errorf("locate canonical git repository: %w", err)
			}
			formatOut, err := source.Run(ctx, "rev-parse", "--show-object-format")
			if err != nil {
				return fmt.Errorf("read git repository object format: %w", err)
			}
			if objectFormat := strings.TrimSpace(string(formatOut)); objectFormat != header.ObjectFormat {
				return fmt.Errorf("git bundle object format is %s, repository object format is %s", header.ObjectFormat, objectFormat)
			}
			for _, prerequisite := range prerequisites {
				out, err := source.Run(ctx, "rev-parse", "--verify", prerequisite.SHA+"^{commit}")
				if err != nil || strings.TrimSpace(string(out)) != prerequisite.SHA {
					return fmt.Errorf("git bundle prerequisite %s is not available from the repository", prerequisite.SHA)
				}
			}

			return MountRef(ctx, bkref, func(root string, _ *mount.Mount) error {
				if _, err := runGitEnv(ctx, root, nil, "init", "--bare", "--quiet", "--object-format="+header.ObjectFormat); err != nil {
					return fmt.Errorf("initialize git bundle repository: %w", err)
				}
				for i, prerequisite := range prerequisites {
					dst := "refs/dagger/bundle/prerequisites/" + strconv.Itoa(i)
					if _, err := runGitEnv(ctx, root, nil, "fetch", "--quiet", "--no-tags", sourceURL, prerequisite.SHA+":"+dst); err != nil {
						return fmt.Errorf("fetch git bundle prerequisite %s: %w", prerequisite.SHA, err)
					}
				}
				if err := verifyGitBundleInRepo(ctx, root, bundlePath); err != nil {
					return fmt.Errorf("verify git bundle: %w", err)
				}
				if err := fetchGitBundleRefs(ctx, root, bundlePath, header.Refs); err != nil {
					return fmt.Errorf("import git bundle: %w", err)
				}
				if _, err := runGitEnv(ctx, root, nil, "pack-refs", "--all"); err != nil {
					return fmt.Errorf("normalize git bundle refs: %w", err)
				}
				return normalizeCanonicalGitDir(root)
			})
		})
		if mountErr != nil {
			return fmt.Errorf("import git bundle prerequisites %s: %w", strings.Join(bundle.PrerequisiteSHAs, ", "), mountErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	snapshot, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snapshot)
	return dir, nil
}

func verifyGitBundleInRepo(ctx context.Context, repoDir, bundlePath string) error {
	_, err := runGitEnv(ctx, repoDir, nil, "bundle", "verify", bundlePath)
	return err
}

func fetchGitBundleRefspecs(ctx context.Context, repoDir, bundlePath string, refspecs []string) error {
	args := []string{"fetch", "--quiet", "--no-tags", "--update-head-ok", bundlePath}
	args = append(args, refspecs...)
	_, err := runGitEnv(ctx, repoDir, nil, args...)
	return err
}

func fetchGitBundleRefs(ctx context.Context, repoDir, bundlePath string, refs []*GitBundleRef) error {
	refspecs := make([]string, 0, len(refs))
	for i, ref := range refs {
		if ref == nil || ref.Name == "" {
			return fmt.Errorf("git bundle contains an unnamed ref")
		}
		dst := ref.Name
		if !strings.HasPrefix(dst, "refs/") {
			dst = "refs/dagger/bundle/imported/" + strconv.Itoa(i)
		}
		refspecs = append(refspecs, "+"+ref.Name+":"+dst)
	}
	if err := fetchGitBundleRefspecs(ctx, repoDir, bundlePath, refspecs); err != nil {
		return err
	}
	for i, ref := range refs {
		dst := ref.Name
		if !strings.HasPrefix(dst, "refs/") {
			dst = "refs/dagger/bundle/imported/" + strconv.Itoa(i)
		}
		out, err := runGitEnv(ctx, repoDir, nil, "rev-parse", "--verify", dst+"^{object}")
		if err != nil || strings.TrimSpace(out) != ref.SHA {
			return fmt.Errorf("imported git bundle ref %q does not resolve to %s", ref.Name, ref.SHA)
		}
	}
	return nil
}
