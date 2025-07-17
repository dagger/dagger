package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/buildkit"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
)

var errEmptyResultRef = fmt.Errorf("empty result reference")

type ExecInMountSaveOpt func() (string, bool)

func SaveResultsWithDesc(format string, a ...any) ExecInMountSaveOpt {
	return func() (string, bool) {
		return fmt.Sprintf(format, a...), true
	}
}

func ImmutableOp() (string, bool) {
	return "", false
}

type FileOrDirectory interface {
	*File | *Directory
	getResult() bkcache.ImmutableRef
	setResult(bkcache.ImmutableRef)
	Evaluatable
}

// execInMount is a helper used by Directory.execInMount and File.execInMount
func execInMount[T FileOrDirectory](ctx context.Context, obj T, saveResultsOpt ExecInMountSaveOpt, f func(string) error) (T, error) {
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

	cacheDesc, saveResults := saveResultsOpt()

	var mountRef bkcache.Ref
	var newRef bkcache.MutableRef
	if saveResults {
		if cacheDesc == "" {
			panic("mutable op without cache description")
		}
		newRef, err = query.BuildkitCache().New(ctx, parentRef, bkSessionGroup,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription(cacheDesc))
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
	if saveResults {
		snap, err := newRef.Commit(ctx)
		if err != nil {
			return nil, err
		}
		obj.setResult(snap)
		return obj, nil
	}
	return obj, nil
}

func getRefOrEvaluate[T FileOrDirectory](ctx context.Context, t T) (bkcache.ImmutableRef, error) {
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
