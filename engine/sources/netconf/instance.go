package netconf

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/network"
	"github.com/docker/docker/libnetwork/resolvconf"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/source"
	"github.com/opencontainers/go-digest"
)

type Instance struct {
	id      *Identifier
	cache   cache.Accessor
	baseCfg *oci.DNSConfig
}

var _ source.SourceInstance = (*Instance)(nil)

func (i *Instance) CacheKey(ctx context.Context, g session.Group, index int) (string, string, solver.CacheOpts, bool, error) {
	dt, err := json.Marshal(i.id)
	if err != nil {
		return "", "", nil, false, err
	}

	dgst := digest.FromBytes(dt).String()

	return "random:netcfg:" + dgst, dgst, nil, true, nil
}

func (i *Instance) Snapshot(ctx context.Context, g session.Group) (_ cache.ImmutableRef, rerr error) {
	newRef, err := i.cache.New(ctx, nil, g, cache.CachePolicyRetain, cache.WithDescription("netconf"))
	if err != nil {
		return nil, fmt.Errorf("new cache: %w", err)
	}

	defer func() {
		if rerr != nil && newRef != nil {
			newRef.Release(context.Background())
		}
	}()

	mount, err := newRef.Mount(ctx, false, g)
	if err != nil {
		return nil, fmt.Errorf("ref mount: %w", err)
	}

	lm := snapshot.LocalMounter(mount)

	dir, err := lm.Mount()
	if err != nil {
		return nil, fmt.Errorf("local mount: %w", err)
	}

	defer func() {
		if rerr != nil && lm != nil {
			lm.Unmount()
		}
	}()

	resolvPath := filepath.Join(dir, "resolv.conf")
	if err := i.generateResolv(resolvPath, i.netCfg()); err != nil {
		return nil, fmt.Errorf("generate resolv: %w", err)
	}

	if err := lm.Unmount(); err != nil {
		return nil, fmt.Errorf("local unmount: %w", err)
	}

	lm = nil

	ref, err := newRef.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	newRef = nil

	return ref, nil
}

func (i *Instance) netCfg() *oci.DNSConfig {
	return &oci.DNSConfig{
		Nameservers: i.baseCfg.Nameservers,
		SearchDomains: append(
			[]string{
				network.SessionDomain(i.id.SessionID),
				// TODO: append parents
			},
			i.baseCfg.SearchDomains...,
		),
		Options: i.baseCfg.Options,
	}
}

func (i *Instance) generateResolv(fp string, dns *oci.DNSConfig) error {
	_, err := resolvconf.Build(fp, dns.Nameservers, dns.SearchDomains, dns.Options)
	return err
}
