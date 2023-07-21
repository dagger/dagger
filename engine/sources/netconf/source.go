package netconf

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	"github.com/pkg/errors"
)

const Scheme = "netconf"

type Source struct {
	cache   cache.Accessor
	baseCfg *oci.DNSConfig
}

type Opt struct {
	CacheAccessor cache.Accessor
	BaseDNSConfig *oci.DNSConfig
}

func NewSource(opt Opt) (source.Source, error) {
	return &Source{
		cache:   opt.CacheAccessor,
		baseCfg: opt.BaseDNSConfig,
	}, nil
}

var _ source.Source = (*Source)(nil)

func (s *Source) ID() string {
	return Scheme
}

func (s *Source) Schemes() []string {
	return []string{Scheme}
}

func (s *Source) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id := &Identifier{}

	if search, ok := attrs[AttrSearchDomains]; ok {
		id.SearchDomains = strings.Fields(search)
	} else {
		return nil, fmt.Errorf("missing %q attribute", AttrSearchDomains)
	}

	return id, nil
}

func (s *Source) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	netId, ok := id.(*Identifier)
	if !ok {
		return nil, errors.Errorf("invalid git identifier %v", id)
	}

	return &Instance{
		id:      netId,
		cache:   s.cache,
		baseCfg: s.baseCfg,
	}, nil
}
