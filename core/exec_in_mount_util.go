package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
)

var errEmptyResultRef = fmt.Errorf("empty result reference")

type execInMountOpt struct {
	commitSnapshot bool
	cacheDesc      string
}

type execInMountOptFn func(opt *execInMountOpt)

func withSavedSnapshot(format string, a ...any) execInMountOptFn {
	return func(opt *execInMountOpt) {
		opt.cacheDesc = fmt.Sprintf(format, a...)
		opt.commitSnapshot = true
	}
}

type fileOrDirectory interface {
	*File | *Directory
	getResult() bkcache.ImmutableRef
	setResult(bkcache.ImmutableRef)
	Evaluatable
}

// execInMount is a helper used by Directory.execInMount and File.execInMount
func execInMount[T fileOrDirectory](ctx context.Context, obj T, f func(string) error, optFns ...execInMountOptFn) (T, error) {
	var saveOpt execInMountOpt
	for _, optFn := range optFns {
		optFn(&saveOpt)
	}

	parentRef, err := getRefOrEvaluate(ctx, obj)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	var mountRef bkcache.Ref
	var newRef bkcache.MutableRef
	if saveOpt.commitSnapshot {
		if saveOpt.cacheDesc == "" {
			return nil, fmt.Errorf("execInMount saveSnapshotOpt missing cache description")
		}
		newRef, err = query.BuildkitCache().New(ctx, parentRef, bkSessionGroup,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription(saveOpt.cacheDesc))
		if err != nil {
			return nil, err
		}
		mountRef = newRef
	} else {
		if parentRef == nil {
			return nil, errEmptyResultRef
		}
		mountRef = parentRef
	}
	err = MountRef(ctx, mountRef, bkSessionGroup, f)
	if err != nil {
		return nil, err
	}
	if saveOpt.commitSnapshot {
		snap, err := newRef.Commit(ctx)
		if err != nil {
			return nil, err
		}
		obj.setResult(snap)
		return obj, nil
	}
	return obj, nil
}

func getRefOrEvaluate[T fileOrDirectory](ctx context.Context, t T) (bkcache.ImmutableRef, error) {
	ref := t.getResult()
	if ref != nil {
		return ref, nil
	}
	res, err := t.Evaluate(ctx)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	cacheRef, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	if cacheRef == nil {
		return nil, nil
	}
	return cacheRef.CacheRef(ctx)
}
