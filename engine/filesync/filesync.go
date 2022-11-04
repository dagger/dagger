package filesync

import (
	io "io"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	keyOverrideExcludes   = "override-excludes"
	keyIncludePatterns    = "include-patterns"
	keyExcludePatterns    = "exclude-patterns"
	keyFollowPaths        = "followpaths"
	keyDirName            = "dir-name"
	keyExporterMetaPrefix = "exporter-md-"
)

type fsSyncProvider struct {
	dirs   DirSource
	p      progressCb
	doneCh chan error
}

type SyncedDir struct {
	Dir      string
	Excludes []string
	Map      func(string, *fstypes.Stat) bool
}

type DirSource interface {
	LookupDir(string) (SyncedDir, bool)
}

type StaticDirSource map[string]SyncedDir

var _ DirSource = StaticDirSource{}

func (dirs StaticDirSource) LookupDir(name string) (SyncedDir, bool) {
	dir, found := dirs[name]
	return dir, found
}

// NewFSSyncProvider creates a new provider for sending files from client
func NewFSSyncProvider(dirs DirSource) *fsSyncProvider {
	return &fsSyncProvider{
		dirs: dirs,
	}
}

func (sp *fsSyncProvider) Register(server *grpc.Server) {
	RegisterFileSyncServer(server, sp)
}

func (sp *fsSyncProvider) DiffCopy(stream FileSync_DiffCopyServer) error {
	return sp.handle("diffcopy", stream)
}
func (sp *fsSyncProvider) TarStream(stream FileSync_TarStreamServer) error {
	return sp.handle("tarstream", stream)
}

func (sp *fsSyncProvider) handle(method string, stream grpc.ServerStream) (retErr error) {
	var pr protocol
	var found bool
	for _, p := range supportedProtocols {
		if method == p.name {
			pr = p
			found = true
			break
		}
	}
	if !found {
		return InvalidSessionError{errors.New("failed to negotiate protocol")}
	}

	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	dirName := ""
	name, ok := opts[keyDirName]
	if ok && len(name) > 0 {
		dirName = name[0]
	}

	dir, ok := sp.dirs.LookupDir(dirName)
	if !ok {
		return InvalidSessionError{status.Errorf(codes.NotFound, "no access allowed to dir %q", dirName)}
	}

	excludes := opts[keyExcludePatterns]
	if len(dir.Excludes) != 0 && (len(opts[keyOverrideExcludes]) == 0 || opts[keyOverrideExcludes][0] != "true") {
		excludes = dir.Excludes
	}
	includes := opts[keyIncludePatterns]

	followPaths := opts[keyFollowPaths]

	var progress progressCb
	if sp.p != nil {
		progress = sp.p
		sp.p = nil
	}

	var doneCh chan error
	if sp.doneCh != nil {
		doneCh = sp.doneCh
		sp.doneCh = nil
	}
	err := pr.sendFn(stream, fsutil.NewFS(dir.Dir, &fsutil.WalkOpt{
		ExcludePatterns: excludes,
		IncludePatterns: includes,
		FollowPaths:     followPaths,
		Map:             dir.Map,
	}), progress)
	if doneCh != nil {
		if err != nil {
			doneCh <- err
		}
		close(doneCh)
	}
	return err
}

func (sp *fsSyncProvider) SetNextProgressCallback(f func(int, bool), doneCh chan error) {
	sp.p = f
	sp.doneCh = doneCh
}

type progressCb func(int, bool)

type protocol struct {
	name   string
	sendFn func(stream Stream, fs fsutil.FS, progress progressCb) error
	recvFn func(stream grpc.ClientStream, destDir string, cu CacheUpdater, progress progressCb, differ fsutil.DiffType, mapFunc func(string, *fstypes.Stat) bool) error
}

var supportedProtocols = []protocol{
	{
		name:   "diffcopy",
		sendFn: sendDiffCopy,
		recvFn: recvDiffCopy,
	},
}

// FSSendRequestOpt defines options for FSSend request
type FSSendRequestOpt struct {
	Name             string
	IncludePatterns  []string
	ExcludePatterns  []string
	FollowPaths      []string
	OverrideExcludes bool // deprecated: this is used by docker/cli for automatically loading .dockerignore from the directory
	DestDir          string
	CacheUpdater     CacheUpdater
	ProgressCb       func(int, bool)
	Filter           func(string, *fstypes.Stat) bool
	Differ           fsutil.DiffType
}

// CacheUpdater is an object capable of sending notifications for the cache hash changes
type CacheUpdater interface {
	MarkSupported(bool)
	HandleChange(fsutil.ChangeKind, string, os.FileInfo, error) error
	ContentHasher() fsutil.ContentHasher
}

// NewFSSyncTargetDir allows writing into a directory
func NewFSSyncTargetDir(outdir string) *fsSyncTarget {
	p := &fsSyncTarget{
		outdir: outdir,
	}
	return p
}

// NewFSSyncTarget allows writing into an io.WriteCloser
func NewFSSyncTarget(f func(map[string]string) (io.WriteCloser, error)) *fsSyncTarget {
	p := &fsSyncTarget{
		f: f,
	}
	return p
}

type fsSyncTarget struct {
	outdir string
	f      func(map[string]string) (io.WriteCloser, error)
}

func (sp *fsSyncTarget) Register(server *grpc.Server) {
	RegisterFileSendServer(server, sp)
}

func (sp *fsSyncTarget) DiffCopy(stream FileSend_DiffCopyServer) (err error) {
	if sp.outdir != "" {
		return syncTargetDiffCopy(stream, sp.outdir)
	}

	if sp.f == nil {
		return errors.New("empty outfile and outdir")
	}
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	md := map[string]string{}
	for k, v := range opts {
		if strings.HasPrefix(k, keyExporterMetaPrefix) {
			md[strings.TrimPrefix(k, keyExporterMetaPrefix)] = strings.Join(v, ",")
		}
	}
	wc, err := sp.f(md)
	if err != nil {
		return err
	}
	if wc == nil {
		return status.Errorf(codes.AlreadyExists, "target already exists")
	}
	defer func() {
		err1 := wc.Close()
		if err != nil {
			err = err1
		}
	}()
	return writeTargetFile(stream, wc)
}

type InvalidSessionError struct {
	err error
}

func (e InvalidSessionError) Error() string {
	return e.err.Error()
}

func (e InvalidSessionError) Unwrap() error {
	return e.err
}
