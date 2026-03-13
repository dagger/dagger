package client

import (
	"context"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/fsutil"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/dagger/dagger/util/patternmatcher"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/metadata"
	"github.com/moby/sys/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/util/fsxutil"
	"github.com/dagger/dagger/util/grpcutil"
)

// extractTraceContext extracts W3C trace context from gRPC incoming metadata,
// returning a context with the remote span as parent. This allows client-side
// code to create child spans of engine-side operations.
func extractTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	return telemetry.Propagator.Extract(ctx, metadataCarrier(md))
}

// metadataCarrier adapts gRPC metadata.MD to propagation.TextMapCarrier.
// Unlike propagation.HeaderCarrier (which wraps http.Header and title-cases
// keys), this keeps keys lowercase as required by gRPC metadata.
type metadataCarrier metadata.MD

func (mc metadataCarrier) Get(key string) string {
	vals := metadata.MD(mc).Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (mc metadataCarrier) Set(key, value string) {
	metadata.MD(mc).Set(key, value)
}

func (mc metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(mc))
	for k := range mc {
		keys = append(keys, k)
	}
	return keys
}

type Filesyncer struct {
	uid, gid uint32
}

func NewFilesyncer() (Filesyncer, error) {
	f := Filesyncer{
		uid: uint32(os.Getuid()),
		gid: uint32(os.Getgid()),
	}

	return f, nil
}

func (f Filesyncer) AsSource() FilesyncSource {
	return FilesyncSource(f)
}

func (f Filesyncer) AsTarget() FilesyncTarget {
	return FilesyncTarget(f)
}

type FilesyncSource Filesyncer

func (s FilesyncSource) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s FilesyncSource) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return fmt.Errorf("tarstream not supported")
}

func (s FilesyncSource) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	ctx := extractTraceContext(stream.Context())

	opts, err := engine.LocalImportOptsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("get local import opts: %w", err)
	}

	absPath, err := Filesyncer(s).fullRootPathAndBaseName(opts.Path, opts.StatResolvePath)
	if err != nil {
		return fmt.Errorf("get full root path: %w", err)
	}

	switch {
	case opts.GitBranchDetect:
		cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
		cmd.Dir = absPath
		out, err := cmd.Output()
		if err != nil {
			return stream.SendMsg(&filesync.BytesMessage{Data: []byte("HEAD")})
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: bytes.TrimSpace(out)})

	case opts.GitRevParseHead:
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = absPath
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: bytes.TrimSpace(out)})

	case opts.GitAddAndCommit != nil:
		cmd := exec.CommandContext(ctx, "git", "add", "-A")
		cmd.Dir = absPath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git add -A: %w: %s", err, out)
		}
		cmd = exec.CommandContext(ctx, "git", "commit", "--allow-empty", "-m", opts.GitAddAndCommit.Message)
		cmd.Dir = absPath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git commit: %w: %s", err, out)
		}
		// Return the commit hash
		cmd = exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = absPath
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: bytes.TrimSpace(out)})

	case opts.GitWorktreeAdd != nil:
		wopts := opts.GitWorktreeAdd
		wtPath := wopts.WorktreePath
		if !filepath.IsAbs(wtPath) {
			wtPath = filepath.Join(absPath, wtPath)
		}

		// Check if worktree already exists and is valid
		if _, err := os.Stat(filepath.Join(wtPath, ".git")); err == nil {
			// Worktree directory exists, verify it's valid
			cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
			cmd.Dir = wtPath
			if err := cmd.Run(); err == nil {
				// Valid worktree, return as-is
				absWT, _ := filepath.Abs(wtPath)
				return stream.SendMsg(&filesync.BytesMessage{Data: []byte(absWT)})
			}
			// Invalid, remove and recreate
			os.RemoveAll(wtPath)
		}

		// Try to add worktree for existing branch
		cmd := exec.CommandContext(ctx, "git", "worktree", "add", wtPath, wopts.Branch)
		cmd.Dir = absPath
		if err := cmd.Run(); err != nil {
			// Branch doesn't exist, create it
			cmd = exec.CommandContext(ctx, "git", "worktree", "add", "-b", wopts.Branch, wtPath)
			cmd.Dir = absPath
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git worktree add -b %s %s: %w: %s", wopts.Branch, wtPath, err, out)
			}
		}

		absWT, _ := filepath.Abs(wtPath)
		return stream.SendMsg(&filesync.BytesMessage{Data: []byte(absWT)})

	case opts.GetAbsPathOnly:
		return stream.SendMsg(&fstypes.Stat{
			Path: filepath.ToSlash(absPath),
		})
	case opts.StatPathOnly:
		var stat *fstypes.Stat
		if opts.StatFollowSymlinks {
			// Use os.Stat to follow symlinks
			fi, err := os.Stat(absPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return status.Errorf(codes.NotFound, "stat path: %s", err)
				}
				return fmt.Errorf("stat path: %w", err)
			}
			stat = &fstypes.Stat{
				Path:    filepath.Base(absPath),
				Mode:    uint32(fi.Mode()),
				Size_:   fi.Size(),
				ModTime: fi.ModTime().UnixNano(),
			}
		} else {
			var err error
			stat, err = fsutil.Stat(absPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return status.Errorf(codes.NotFound, "stat path: %s", err)
				}
				return fmt.Errorf("stat path: %w", err)
			}
		}

		if opts.StatReturnAbsPath {
			stat.Path = absPath
		}

		stat.Path = filepath.ToSlash(stat.Path)
		return stream.SendMsg(stat)

	case opts.SearchOpts != nil:
		// Run ripgrep (or grep fallback) on the host
		results, err := searchHostPath(ctx, absPath, opts.SearchOpts)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		data, err := json.Marshal(results)
		if err != nil {
			return fmt.Errorf("marshal search results: %w", err)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: data})

	case opts.GlobPattern != "":
		// Walk the directory and match files against the glob pattern
		matches, err := globHostPath(absPath, opts.GlobPattern)
		if err != nil {
			return fmt.Errorf("glob: %w", err)
		}
		data, err := json.Marshal(matches)
		if err != nil {
			return fmt.Errorf("marshal glob results: %w", err)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: data})

	case opts.ReadSingleFileOnly:
		// just stream the file bytes to the caller
		fileContents, err := os.ReadFile(absPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "read path: %s", err)
			}
			return fmt.Errorf("read file: %w", err)
		}
		if len(fileContents) > int(opts.MaxFileSize) {
			// NOTE: can lift this size restriction by chunking if ever needed
			return fmt.Errorf("file contents too large: %d > %d", len(fileContents), opts.MaxFileSize)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: fileContents})

	default:
		// otherwise, do the whole directory sync back to the caller
		fs, err := fsutil.NewFS(absPath)
		if err != nil {
			return err
		}
		filteredFS, err := fsutil.NewFilterFS(fs, &fsutil.FilterOpt{
			IncludePatterns: opts.IncludePatterns,
			ExcludePatterns: opts.ExcludePatterns,
			FollowPaths:     opts.FollowPaths,
			Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
				st.Uid = 0
				st.Gid = 0
				return fsutil.MapResultKeep
			},
		})
		if opts.UseGitIgnore {
			filteredFS, err = fsxutil.NewGitIgnoreMarkedFS(filteredFS, fsxutil.NewGitIgnoreMatcher(fs))
			if err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
		return fsutil.Send(stream.Context(), stream, filteredFS, nil)
	}
}

type FilesyncTarget Filesyncer

func (t FilesyncTarget) Register(server *grpc.Server) {
	filesync.RegisterFileSendServer(server, t)
}

func (t FilesyncTarget) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local export opts: %w", err)
	}


	absPath, err := Filesyncer(t).fullRootPathAndBaseName(opts.Path, false)
	if err != nil {
		return fmt.Errorf("get full root path: %w", err)
	}

	for _, removePath := range opts.RemovePaths {
		isDir := strings.HasSuffix(removePath, "/")
		if !filepath.IsAbs(removePath) {
			removePath = filepath.Join(opts.Path, removePath)
		}
		if isDir {
			if err := os.RemoveAll(removePath); err != nil {
				return fmt.Errorf("remove path %s: %w", removePath, err)
			}
		} else {
			if err := os.Remove(removePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove path %s: %w", removePath, err)
			}
		}
	}

	if !opts.IsFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := user.MkdirAllAndChown(filepath.FromSlash(absPath), 0o700, int(t.uid), int(t.gid), user.WithOnlyNew); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", absPath, err)
		}

		err := fsutil.Receive(stream.Context(), stream, absPath, fsutil.ReceiveOpt{
			Merge: opts.Merge,
			Filter: func(path string, stat *fstypes.Stat) bool {
				stat.Uid = t.uid
				stat.Gid = t.gid
				return true
			},
		})
		if err != nil {
			return fmt.Errorf("failed to receive fs changes: %w", err)
		}


		return nil
	}

	// This is either a file export or a container tarball export, we'll just be receiving BytesMessages with
	// the contents and can write them directly to the destination path.

	// If the dest is a directory that already exists, we will never delete it and replace it with the file.
	// However, if allowParentDirPath is set, we will write the file underneath that existing directory.
	// But if allowParentDirPath is not set, which is the default setting in our API right now, we will return
	// an error when path is a pre-existing directory.
	allowParentDirPath := opts.AllowParentDirPath

	// File exports specifically (as opposed to container tar exports) have an original filename that we will
	// use in the case where dest is a directory and allowParentDirPath is set, in which case we need to know
	// what to name the file underneath the pre-existing directory.
	fileOriginalName := opts.FileOriginalName

	var destParentDir string
	var finalDestPath string
	stat, err := os.Stat(absPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(absPath)
		finalDestPath = absPath
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", absPath, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(absPath)
		finalDestPath = absPath
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", absPath)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if fileOriginalName == "" {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", absPath)
		}
		destParentDir = absPath
		finalDestPath = filepath.Join(destParentDir, fileOriginalName)
	}

	if err := user.MkdirAllAndChown(filepath.FromSlash(destParentDir), 0o700, int(t.uid), int(t.gid), user.WithOnlyNew); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", absPath, err)
	}

	if opts.FileMode == 0 {
		opts.FileMode = 0o600
	}
	destF, err := os.OpenFile(finalDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, opts.FileMode)
	if err != nil {
		return fmt.Errorf("failed to create synctarget dest file %s: %w", finalDestPath, err)
	}
	defer destF.Close()
	if runtime.GOOS != "windows" {
		if err := destF.Chown(int(t.uid), int(t.gid)); err != nil {
			return fmt.Errorf("failed to chown synctarget dest file %s: %w", finalDestPath, err)
		}
	}

	for {
		msg := filesync.BytesMessage{}
		if err := stream.RecvMsg(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := destF.Write(msg.Data); err != nil {
			return err
		}
	}
}

func (f Filesyncer) fullRootPathAndBaseName(reqPath string, fullyResolvePath bool) (_ string, err error) {
	// NOTE: filepath.Clean also handles calling FromSlash (relevant when this is a Windows client)
	reqPath = filepath.Clean(reqPath)

	if home, err := os.UserHomeDir(); err == nil {
		if p, err := pathutil.ExpandHomeDir(home, reqPath); err == nil {
			reqPath = p
		}
	}

	rootPath, err := pathutil.Abs(reqPath)
	if err != nil {
		return "", fmt.Errorf("get abs path: %w", err)
	}
	if fullyResolvePath {
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", status.Errorf(codes.NotFound, "eval symlinks: %s", err)
			}
			return "", fmt.Errorf("eval symlinks: %w", err)
		}
	}
	return rootPath, nil
}

type FilesyncSourceProxy struct {
	Client filesync.FileSyncClient
}

func (s FilesyncSourceProxy) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s FilesyncSourceProxy) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return fmt.Errorf("tarstream not supported")
}

func (s FilesyncSourceProxy) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(errors.New("proxy filesync source done"))

	clientStream, err := s.Client.DiffCopy(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("create client filesync source diffcopy stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, stream)
}

// searchHostPath runs ripgrep (or falls back to grep) on the host filesystem
// and returns structured search results.
func searchHostPath(ctx context.Context, root string, opts *engine.LocalSearchOpts) ([]engine.LocalSearchResult, error) {
	rgPath, err := exec.LookPath("rg")
	if err == nil {
		return searchWithRipgrep(ctx, root, rgPath, opts)
	}
	return searchWithGrep(ctx, root, opts)
}

// ripgrep JSON output types
type rgJSON struct {
	Type string `json:"type"`
	Data struct {
		Path           rgContent `json:"path"`
		Lines          rgContent `json:"lines"`
		LineNumber     int       `json:"line_number"`
		AbsoluteOffset int       `json:"absolute_offset"`
		Submatches     []struct {
			Match rgContent `json:"match"`
			Start int       `json:"start"`
			End   int       `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

type rgContent struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}

func searchWithRipgrep(ctx context.Context, root string, rgPath string, opts *engine.LocalSearchOpts) (results []engine.LocalSearchResult, rerr error) {
	var args []string
	if opts.Literal {
		args = append(args, "--fixed-strings")
	}
	if opts.Multiline {
		args = append(args, "--multiline")
	}
	if opts.Dotall {
		args = append(args, "--multiline-dotall")
	}
	if opts.Insensitive {
		args = append(args, "--ignore-case")
	}
	if !opts.SkipIgnored {
		args = append(args, "--no-ignore")
	}
	if !opts.SkipHidden {
		args = append(args, "--hidden")
	}
	if opts.FilesOnly {
		args = append(args, "--files-with-matches")
	} else {
		args = append(args, "--json")
	}
	args = append(args, "--regexp="+opts.Pattern)
	args = append(args, "--no-follow")

	for _, glob := range opts.Globs {
		args = append(args, "--glob="+glob)
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}

	cmd := exec.Command(rgPath, args...)
	cmd.Dir = root

	// Create an OTel span for the command execution
	ctx, span := otel.Tracer(InstrumentationLibrary).Start(ctx, "exec "+strings.Join(cmd.Args, " "),
		telemetry.Encapsulated())
	defer telemetry.EndWithCause(span, &rerr)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(&errBuf, stdio.Stderr)

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer out.Close()

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Tee stdout to the span for observability
	stdoutReader := io.TeeReader(out, stdio.Stdout)

	var parseErr error

	if opts.FilesOnly {
		scan := bufio.NewScanner(stdoutReader)
		for scan.Scan() {
			line := scan.Text()
			if line == "" {
				continue
			}
			results = append(results, engine.LocalSearchResult{FilePath: line})
		}
		parseErr = scan.Err()
	} else {
		dec := json.NewDecoder(stdoutReader)
		for {
			var match rgJSON
			if err := dec.Decode(&match); err != nil {
				if err == io.EOF {
					break
				}
				parseErr = err
				break
			}
			if match.Type != "match" {
				continue
			}
			data := match.Data
			// Skip non-utf8 content
			if len(data.Path.Bytes) > 0 || len(data.Lines.Bytes) > 0 {
				continue
			}

			result := engine.LocalSearchResult{
				FilePath:       data.Path.Text,
				LineNumber:     data.LineNumber,
				AbsoluteOffset: data.AbsoluteOffset,
				MatchedLines:   data.Lines.Text,
			}
			for _, sm := range data.Submatches {
				result.Submatches = append(result.Submatches, engine.LocalSearchSubmatch{
					Text:  sm.Match.Text,
					Start: sm.Start,
					End:   sm.End,
				})
			}
			results = append(results, result)
			if opts.Limit != nil && len(results) >= *opts.Limit {
				break
			}
		}
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
			// Exit code 1 means no matches
			return []engine.LocalSearchResult{}, nil
		}
		if parseErr != nil {
			return nil, errors.Join(parseErr, waitErr)
		}
		if errBuf.Len() > 0 {
			return nil, fmt.Errorf("ripgrep error: %s", errBuf.String())
		}
		return nil, waitErr
	}
	if parseErr != nil {
		return nil, parseErr
	}

	if results == nil {
		results = []engine.LocalSearchResult{}
	}
	return results, nil
}

func searchWithGrep(ctx context.Context, root string, opts *engine.LocalSearchOpts) (results []engine.LocalSearchResult, rerr error) {
	var args []string
	args = append(args, "-r") // recursive
	args = append(args, "-n") // line numbers
	args = append(args, "-b") // byte offset

	if opts.Literal {
		args = append(args, "-F") // fixed strings
	} else {
		args = append(args, "-E") // extended regex
	}
	if opts.Insensitive {
		args = append(args, "-i")
	}
	if opts.FilesOnly {
		args = append(args, "-l") // files only
	}
	// Note: grep doesn't support --multiline, --dotall, --hidden, --no-ignore,
	// or --glob natively. We do our best.

	for _, glob := range opts.Globs {
		args = append(args, "--include="+glob)
	}

	args = append(args, "-e", opts.Pattern)

	if len(opts.Paths) > 0 {
		args = append(args, opts.Paths...)
	} else {
		args = append(args, ".")
	}

	cmd := exec.Command("grep", args...)
	cmd.Dir = root

	// Create an OTel span for the command execution
	ctx, span := otel.Tracer(InstrumentationLibrary).Start(ctx, "exec "+strings.Join(cmd.Args, " "),
		telemetry.Encapsulated())
	defer telemetry.EndWithCause(span, &rerr)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&outBuf, stdio.Stdout)
	cmd.Stderr = io.MultiWriter(&errBuf, stdio.Stderr)

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// No matches
			return []engine.LocalSearchResult{}, nil
		}
		return nil, fmt.Errorf("grep error: %w", err)
	}

	output := outBuf.String()

	if opts.FilesOnly {
		scan := bufio.NewScanner(strings.NewReader(output))
		for scan.Scan() {
			line := scan.Text()
			if line == "" {
				continue
			}
			// Strip leading "./" if present
			line = strings.TrimPrefix(line, "./")
			results = append(results, engine.LocalSearchResult{FilePath: line})
		}
	} else {
		// Parse grep -rnb output: file:line:offset:content
		scan := bufio.NewScanner(strings.NewReader(output))
		for scan.Scan() {
			line := scan.Text()
			if line == "" {
				continue
			}
			// Format: file:line:byte_offset:matched_line
			parts := strings.SplitN(line, ":", 4)
			if len(parts) < 4 {
				continue
			}
			filePath := strings.TrimPrefix(parts[0], "./")
			lineNum := 0
			fmt.Sscanf(parts[1], "%d", &lineNum)
			byteOffset := 0
			fmt.Sscanf(parts[2], "%d", &byteOffset)
			matchedLine := parts[3]

			results = append(results, engine.LocalSearchResult{
				FilePath:       filePath,
				LineNumber:     lineNum,
				AbsoluteOffset: byteOffset,
				MatchedLines:   matchedLine + "\n",
			})
			if opts.Limit != nil && len(results) >= *opts.Limit {
				break
			}
		}
	}

	if results == nil {
		results = []engine.LocalSearchResult{}
	}
	return results, nil
}

// globHostPath walks the directory at root and returns paths matching the
// given glob pattern. The returned paths are relative to root and use forward
// slashes. Directories are suffixed with "/".
func globHostPath(root string, pattern string) ([]string, error) {
	pat, err := patternmatcher.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}

	// Optimization: if the pattern has no wildcards, we can skip subtrees
	// that can't possibly match.
	patternChars := "*[]?^"
	if filepath.Separator != '\\' {
		patternChars += `\`
	}
	patStr := pat.String()
	// Strip trailing ** or * glob
	for strings.HasSuffix(patStr, string(filepath.Separator)+"**") {
		patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"**")
	}
	patStr = strings.TrimSuffix(patStr, "**")
	for strings.HasSuffix(patStr, string(filepath.Separator)+"*") {
		patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"*")
	}
	patStr = strings.TrimSuffix(patStr, "*")
	onlyPrefixIncludes := !strings.ContainsAny(patStr, patternChars)

	var matches []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, prevErr error) error {
		if prevErr != nil {
			return prevErr
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		match, err := pat.Match(rel)
		if err != nil {
			return err
		}

		if match {
			result := filepath.ToSlash(rel)
			if d.IsDir() {
				result += "/"
			}
			matches = append(matches, result)
		} else if d.IsDir() && onlyPrefixIncludes {
			dirSlash := rel + string(filepath.Separator)
			if !pat.Exclusion() {
				prefixSlash := patStr + string(filepath.Separator)
				if !strings.HasPrefix(prefixSlash, dirSlash) {
					return filepath.SkipDir
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if matches == nil {
		matches = []string{}
	}
	return matches, nil
}

type FilesyncTargetProxy struct {
	Client filesync.FileSendClient
}

func (s FilesyncTargetProxy) Register(server *grpc.Server) {
	filesync.RegisterFileSendServer(server, s)
}

func (s FilesyncTargetProxy) DiffCopy(stream filesync.FileSend_DiffCopyServer) error {
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(errors.New("proxy filesync target done"))

	clientStream, err := s.Client.DiffCopy(grpcutil.IncomingToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("create client filesync target diffcopy stream: %w", err)
	}

	return grpcutil.ProxyStream[anypb.Any](ctx, clientStream, stream)
}
