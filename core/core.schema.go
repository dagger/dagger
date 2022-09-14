package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
)

var _ router.ExecutableSchema = &coreSchema{}

type coreSchema struct {
	*baseSchema
	sshAuthSockID string
	workdirID     string
}

func (r *coreSchema) Name() string {
	return "core"
}

func (r *coreSchema) Schema() string {
	return `
extend type Query {
	"Core API"
	core: Core!

	"Host API"
	host: Host!
}

"Core API"
type Core {
	"Fetch an OCI image"
	image(ref: String!): Filesystem!

	"Fetch a git repository"
	git(remote: String!, ref: String): Filesystem!

	pushMultiplatformImage(ref: String!, filesystems: [FSID!]!): Boolean!
}

"Interactions with the user's host filesystem"
type Host {
	"Fetch the client's workdir"
	workdir: LocalDir!

	"Fetch a client directory"
	dir(id: String!): LocalDir!
}

"A directory on the user's host filesystem"
type LocalDir {
	"Read the contents of the directory"
	read: Filesystem!

	"Write the provided filesystem to the directory"
	write(contents: FSID!, path: String): Boolean!
}
`
}

func (r *coreSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"core": r.core,
			"host": r.host,
		},
		"Core": router.ObjectResolver{
			"image": r.image,
			"git":   r.git,
			// FIXME: need to find a better place to put this (a filesystem like type that bundle multiple platforms?)
			"pushMultiplatformImage": r.pushMultiplatformImage,
		},
		"Host": router.ObjectResolver{
			"workdir": r.workdir,
			"dir":     r.dir,
		},
		"LocalDir": router.ObjectResolver{
			"read":  r.localDirRead,
			"write": r.localDirWrite,
		},
	}
}

func (r *coreSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (r *coreSchema) core(p graphql.ResolveParams) (any, error) {
	return router.Parent[struct{}](p.Source), nil
}

func (r *coreSchema) host(p graphql.ResolveParams) (any, error) {
	return router.Parent[struct{}](p.Source), nil
}

func (r *coreSchema) pushMultiplatformImage(p graphql.ResolveParams) (any, error) {
	ref, _ := p.Args["ref"].(string)
	if ref == "" {
		return nil, fmt.Errorf("ref is required for pushImage")
	}

	rawFilesystems, _ := p.Args["filesystems"].([]any)
	var filesystems []*filesystem.Filesystem
	for _, raw := range rawFilesystems {
		fsid, ok := raw.(filesystem.FSID)
		if !ok {
			return nil, fmt.Errorf("invalid filesystem: %v", raw)
		}
		fs := &filesystem.Filesystem{ID: fsid}
		filesystems = append(filesystems, fs)
	}

	if err := r.ExportMultplatformImage(p.Context, filesystems, bkclient.ExportEntry{
		Type: bkclient.ExporterImage,
		Attrs: map[string]string{
			"name": ref,
			"push": "true",
		},
	}); err != nil {
		return nil, err
	}
	return true, nil
}

func (r *coreSchema) image(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[struct{}](p.Source)
	ref := p.Args["ref"].(string)

	st := llb.Image(ref)
	fs, err := r.Solve(p.Context, st, parent.Platform)
	if err != nil {
		return nil, err
	}
	return router.WithVal(parent, fs), nil
}

func (r *coreSchema) git(p graphql.ResolveParams) (any, error) {
	// TODO:(sipsma) you could wrap all these methods above so they have a type that's actually nice and skips all the parent boilerplate?
	parent := router.Parent[struct{}](p.Source)
	remote := p.Args["remote"].(string)
	ref, _ := p.Args["ref"].(string)

	var opts []llb.GitOption
	if r.sshAuthSockID != "" {
		opts = append(opts, llb.MountSSHSock(r.sshAuthSockID))
	}
	st := llb.Git(remote, ref, opts...)
	fs, err := r.Solve(p.Context, st, parent.Platform)
	if err != nil {
		return nil, err
	}
	return router.WithVal(parent, fs), nil
}

type localDir struct {
	ID string `json:"id"`
}

func (r *coreSchema) workdir(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[struct{}](p.Source)
	return router.WithVal(parent, localDir{r.workdirID}), nil
}

func (r *coreSchema) dir(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[struct{}](p.Source)
	id := p.Args["id"].(string)
	return router.WithVal(parent, localDir{id}), nil
}

func (r *coreSchema) localDirRead(p graphql.ResolveParams) (any, error) {
	parent := router.Parent[localDir](p.Source)

	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(llb.Copy(llb.Local(
		parent.Val.ID,
		// TODO: better shared key hint?
		llb.SharedKeyHint(parent.Val.ID),
		// FIXME: should not be hardcoded
		llb.ExcludePatterns([]string{"**/node_modules"}),
	), "/", "/"))

	fs, err := r.Solve(p.Context, st, parent.Platform, llb.LocalUniqueID(parent.Val.ID))
	if err != nil {
		return nil, err
	}
	return router.WithVal(parent, fs), nil
}

func (r *coreSchema) localDirWrite(p graphql.ResolveParams) (any, error) {
	fsid := p.Args["contents"].(filesystem.FSID)
	fs := filesystem.Filesystem{ID: fsid}

	workdir, err := filepath.Abs(r.solveOpts.LocalDirs[r.workdirID])
	if err != nil {
		return nil, err
	}

	path, _ := p.Args["path"].(string)
	dest, err := filepath.Abs(filepath.Join(workdir, path))
	if err != nil {
		return nil, err
	}

	// Ensure the destination is a sub-directory of the workdir
	dest, err = filepath.EvalSymlinks(dest)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(dest, workdir) {
		return nil, fmt.Errorf("path %q is outside workdir", path)
	}

	if err := r.Export(p.Context, &fs, bkclient.ExportEntry{
		Type:      bkclient.ExporterLocal,
		OutputDir: dest,
	}); err != nil {
		return nil, err
	}
	return true, nil
}
