package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// Copying objects out of a source repository (typically a remote's mirror,
// borrowed under its lock) into a private destination repository.
//
// A fetch over file:// runs upload-pack in the source and index-pack in the
// destination: index-pack inflates and hashes every object again and resolves
// every delta. pack-objects in the destination, borrowing the source through a
// transient alternate, reuses the source's compressed objects and deltas and
// writes its own index instead. Measured on a warm mirror of dagger/dagger
// (194k objects, 244MB pack, git 2.56, 32 cores), a full-history copy takes
// 1.16s instead of 3.29s. Reachability bitmaps would save another ~0.35s of
// enumeration, but mirrors have none (git's automatic maintenance does not
// write them) and keeping one fresh is its own project. Depth-limited copies
// gain only ~0.2s and keep fetching, which already handles their shallow
// boundaries.

// gitObjectsRunner runs git in a destination repository with extra
// environment and stdin, returning its standard output.
type gitObjectsRunner func(ctx context.Context, env []string, stdin io.Reader, args ...string) (string, error)

// gitCLIObjectsRunner runs in git's repository, keeping its execution setup
// (namespaces, credentials) and adding env on top.
func gitCLIObjectsRunner(git *gitutil.GitCLI) gitObjectsRunner {
	return func(ctx context.Context, env []string, stdin io.Reader, args ...string) (string, error) {
		out, err := git.New(gitutil.WithEnv(env...)).RunWithStdin(ctx, stdin, args...)
		return string(out), err
	}
}

// packGitObjects writes one non-thin pack into destObjects, the destination's
// own object database. With revs, it holds everything reachable from objects
// (pack-objects --revs); otherwise exactly the listed objects. sourceObjects
// is borrowed through a transient alternate (an environment variable, never an
// alternates file), so the destination refers back to nothing afterwards; and
// since pack-objects creates temporary files in its primary object database,
// those stay private too. Replace refs, lazy fetches, and the source's own
// shallow file and grafts never apply: a missing object fails the pack rather
// than silently truncating it.
func packGitObjects(ctx context.Context, run gitObjectsRunner, destObjects, sourceObjects string, objects []string, revs bool) error {
	if !filepath.IsAbs(sourceObjects) {
		return fmt.Errorf("invalid source object directory %q", sourceObjects)
	}
	var input strings.Builder
	for _, object := range objects {
		input.WriteString(object + "\n")
	}
	args := []string{"pack-objects"}
	if revs {
		args = append(args, "--revs")
	}
	args = append(args, "--delta-base-offset", "--quiet", filepath.Join(destObjects, "pack", "pack"))
	env := []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(sourceObjects)}
	_, err := run(ctx, env, strings.NewReader(input.String()), args...)
	return err
}

// gitObjectsPresent reports which of objects the repository has, by its own
// object database alone (git runs without any borrowed alternate).
func gitObjectsPresent(ctx context.Context, git *gitutil.GitCLI, objects []string) (map[string]bool, error) {
	present := make(map[string]bool, len(objects))
	if len(objects) == 0 {
		return present, nil
	}
	out, err := git.RunWithStdin(ctx, strings.NewReader(strings.Join(objects, "\n")+"\n"), "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if oid, missing := strings.CutSuffix(line, " missing"); missing {
			present[oid] = false
		} else if line != "" {
			present[line] = true
		}
	}
	return present, nil
}

// gitPackCopy copies objects from a source repository into a destination
// repository's own object database with packGitObjects.
type gitPackCopy struct {
	dest          *gitutil.GitCLI
	destObjects   string
	sourceObjects string
}

// newGitPackCopy prepares a copy from source into dest, which must already
// exist. It refuses sources whose history git would read differently from
// their objects alone (shallow or grafted: a fetch serves them truncated,
// while a pack walk would see the commits' real parents), shallow
// destinations (the walk would stop at their boundaries), and object format
// mismatches.
func newGitPackCopy(ctx context.Context, dest, source *gitutil.GitCLI) (*gitPackCopy, error) {
	for _, check := range []struct {
		git  *gitutil.GitCLI
		role string
		path string
	}{{source, "source", "shallow"}, {source, "source", "info/grafts"}, {dest, "destination", "shallow"}} {
		out, err := check.git.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", check.path)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(strings.TrimSuffix(string(out), "\n")); err == nil {
			return nil, fmt.Errorf("%s has %s", check.role, check.path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	formats := make([]string, 2)
	objects := make([]string, 2)
	for i, git := range []*gitutil.GitCLI{source, dest} {
		out, err := git.Run(ctx, "rev-parse", "--show-object-format")
		if err != nil {
			return nil, err
		}
		formats[i] = strings.TrimSpace(string(out))
		out, err = git.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "objects")
		if err != nil {
			return nil, err
		}
		objects[i] = strings.TrimSuffix(string(out), "\n")
	}
	if formats[0] != formats[1] {
		return nil, fmt.Errorf("source object format %s differs from destination %s", formats[0], formats[1])
	}
	return &gitPackCopy{dest: dest, sourceObjects: objects[0], destObjects: objects[1]}, nil
}

// pack copies objects (with revs, their complete history) and verifies that
// the destination has every one of them. It writes no refs.
func (c *gitPackCopy) pack(ctx context.Context, objects []string, revs bool) error {
	if err := packGitObjects(ctx, gitCLIObjectsRunner(c.dest), c.destObjects, c.sourceObjects, objects, revs); err != nil {
		return err
	}
	present, err := gitObjectsPresent(ctx, c.dest, objects)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if !present[object] {
			return fmt.Errorf("packed object %s is missing", object)
		}
	}
	return nil
}

// packGitClosure copies the complete history of revs (object IDs) from
// source into dest, which must already exist. It writes no refs.
func packGitClosure(ctx context.Context, dest, source *gitutil.GitCLI, revs []string) error {
	c, err := newGitPackCopy(ctx, dest, source)
	if err != nil {
		return err
	}
	return c.pack(ctx, revs, true)
}

// copyGitObjects runs pack, the pack-objects copy, falling back to fetch on
// any failure other than the caller's own cancellation. discard runs before
// the fallback to drop whatever the failed pack left behind. The method that
// ran ("pack" or "fetch") and any fallback reason are recorded on an internal
// span and as a wcprof io op "git.copy[<method>]", so profiles of a mirror's
// locked phase show which copy held it.
func copyGitObjects(ctx context.Context, what string, pack, discard, fetch func(context.Context) error) (method string, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "copying git objects: "+what, telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	start := wcprof.NowNS()
	defer func() {
		span.SetAttributes(attribute.String("dagger.git.copy.method", method))
		if start == 0 {
			return
		}
		outcome := wcprof.OutcomeOK
		if rerr != nil {
			outcome = wcprof.OutcomeError
		}
		wcprof.RecordOp(ctx, wcprof.OpKindIO, "git.copy["+method+"]", wcprof.OpOpts{WorkType: wcprof.WorkTypeEngine}, start, wcprof.NowNS(), outcome)
	}()
	if pack != nil {
		err := pack(ctx)
		if err == nil {
			return "pack", nil
		}
		if ctx.Err() != nil {
			return "pack", err
		}
		span.SetAttributes(attribute.String("dagger.git.copy.pack_fallback", err.Error()))
		if discard != nil {
			if err := discard(ctx); err != nil {
				return "pack", fmt.Errorf("discard failed pack copy: %w", err)
			}
		}
	}
	return "fetch", fetch(ctx)
}
