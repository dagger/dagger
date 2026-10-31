package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
)

// Compare the selected values under the same content and metadata rules used
// by Directory and File inputs. Git diffs omit some permission changes.
func workspaceReadRegionsEqual(ctx context.Context, srv *dagql.Server, before, after dagql.ObjectResult[*Directory], selectors []provenance.Selector) bool {
	ctx = suppressWorkspaceReads(ctx)
	for _, selector := range selectors {
		old, err := workspaceReadRegionFingerprint(ctx, srv, before, selector)
		if err != nil {
			return false
		}
		current, err := workspaceReadRegionFingerprint(ctx, srv, after, selector)
		if err != nil || old != current {
			return false
		}
	}
	return true
}

func workspaceReadRegionFingerprint(ctx context.Context, srv *dagql.Server, root dagql.ObjectResult[*Directory], selector provenance.Selector) (string, error) {
	stat, err := root.Self().Stat(ctx, root, srv, selector.Path, true)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	selectPath := dagql.Selector{Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(selector.Path)}}}
	switch stat.FileType {
	case FileTypeRegular:
		selectPath.Field = "file"
		var file dagql.ObjectResult[*File]
		if err := srv.Select(ctx, root, &file, selectPath); err != nil {
			return "", err
		}
		digest, err := file.Self().Digest(ctx, file, false)
		return "file:" + digest, err
	case FileTypeDirectory:
		selectPath.Field = "directory"
		var directory dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, root, &directory, selectPath); err != nil {
			return "", err
		}
		if len(selector.Include) > 0 || len(selector.Exclude) > 0 {
			id, err := directory.ID()
			if err != nil {
				return "", err
			}
			var filtered dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, srv.Root(), &filtered,
				dagql.Selector{Field: "directory"},
				dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String("/")},
					{Name: "source", Value: dagql.NewID[*Directory](id)},
					{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(selector.Include...))},
					{Name: "exclude", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(selector.Exclude...))},
				}},
			); err != nil {
				return "", err
			}
			directory = filtered
		}
		digest, err := directory.Self().Digest(ctx, directory)
		return "directory:" + digest, err
	default:
		return "", fmt.Errorf("unsupported workspace input type %q at %q", stat.FileType, selector.Path)
	}
}
