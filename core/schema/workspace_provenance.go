package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
)

type provenancePathArgs struct {
	Path  string
	Kinds []string `default:"[]"`
}
type provenanceDiffArgs struct {
	Changes dagql.ID[*core.Changeset]
	Kinds   []string `default:"[]"`
}

func (args provenanceDiffArgs) matches(ctx context.Context, p *core.WorkspaceProvenance) (bool, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}
	changes, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return false, err
	}
	return p.AffectedByDiff(ctx, changes.Self(), args.Kinds)
}

func installWorkspaceProvenance(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.WorkspaceSelector](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.WorkspaceProvenance](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.WorkspaceSelector]{}.Install(srv)
	dagql.Fields[*core.WorkspaceProvenance]{
		dagql.Func("affectedByPath", func(_ context.Context, p *core.WorkspaceProvenance, args provenancePathArgs) (bool, error) {
			return p.AffectedByPath(args.Path, args.Kinds)
		}).Doc("Whether an input region overlaps this workspace-root path. Directory queries can match conservatively."),
		dagql.Func("affectedByDiff", func(ctx context.Context, p *core.WorkspaceProvenance, args provenanceDiffArgs) (bool, error) {
			return args.matches(ctx, p)
		}).Doc("Whether an input region overlaps paths in this changeset. Paths must use this workspace's boundary."),
	}.Install(srv)
	dagql.Fields[*core.Artifact]{
		dagql.Func("provenance", func(ctx context.Context, a *core.Artifact, _ struct{}) (*core.WorkspaceProvenance, error) {
			return a.Provenance(ctx)
		}).Doc("Inspect possible workspace inputs without evaluating the artifact leaf. This prototype retains conservative root regions for inputs it cannot prove.").DoNotCache("Inspect current receiver construction"),
		dagql.Func("affectedByPath", func(ctx context.Context, a *core.Artifact, args provenancePathArgs) (bool, error) {
			if _, err := provenance.Path(args.Path); err != nil {
				return false, err
			}
			if err := provenance.ValidateKinds(args.Kinds); err != nil {
				return false, err
			}
			p, err := a.Provenance(ctx)
			if err != nil {
				return false, err
			}
			return p.AffectedByPath(args.Path, args.Kinds)
		}).Doc("Whether this artifact may depend on a workspace-root path.").DoNotCache("Inspect current receiver construction"),
		dagql.Func("affectedByDiff", func(ctx context.Context, a *core.Artifact, args provenanceDiffArgs) (bool, error) {
			if err := provenance.ValidateKinds(args.Kinds); err != nil {
				return false, err
			}
			p, err := a.Provenance(ctx)
			if err != nil {
				return false, err
			}
			return args.matches(ctx, p)
		}).Doc("Whether this artifact may depend on changed workspace paths.").DoNotCache("Inspect current receiver construction"),
	}.Install(srv)
	dagql.Fields[*core.Artifacts]{
		dagql.Func("filterAffectedByPath", func(ctx context.Context, a *core.Artifacts, args provenancePathArgs) (*core.Artifacts, error) {
			if _, err := provenance.Path(args.Path); err != nil {
				return nil, err
			}
			if err := provenance.ValidateKinds(args.Kinds); err != nil {
				return nil, err
			}
			return filterAffected(ctx, a, func(p *core.WorkspaceProvenance) (bool, error) { return p.AffectedByPath(args.Path, args.Kinds) })
		}).Doc("Keep artifacts that may depend on this workspace-root path. Enumerates collections but does not run artifact leaves.").DoNotCache("Inspect current receiver construction"),
		dagql.Func("filterAffectedByDiff", func(ctx context.Context, a *core.Artifacts, args provenanceDiffArgs) (*core.Artifacts, error) {
			if err := provenance.ValidateKinds(args.Kinds); err != nil {
				return nil, err
			}
			return filterAffected(ctx, a, func(p *core.WorkspaceProvenance) (bool, error) {
				return args.matches(ctx, p)
			})
		}).Doc("Keep artifacts that may depend on changed workspace paths. Unknown inputs stay selected.").DoNotCache("Inspect current receiver construction"),
	}.Install(srv)
}

func filterAffected(ctx context.Context, a *core.Artifacts, matches func(*core.WorkspaceProvenance) (bool, error)) (*core.Artifacts, error) {
	expanded, err := a.Expand(ctx)
	if err != nil {
		return nil, err
	}
	selected := expanded
	for _, item := range expanded.Entries {
		p, err := item.Provenance(ctx)
		if err != nil {
			return nil, err
		}
		match, err := matches(p)
		if err != nil {
			return nil, err
		}
		if match {
			continue
		}
		uri, err := item.URI(core.ArtifactURIOpts{DimensionKeys: true, Full: true})
		if err != nil {
			return nil, err
		}
		address, err := dagaddress.Parse(uri)
		if err != nil {
			return nil, err
		}
		selected, err = selected.WithoutURI(address)
		if err != nil {
			return nil, err
		}
	}
	return selected, nil
}
