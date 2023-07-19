package netconf

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/session/networks"
	"github.com/dagger/dagger/network"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/source"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type Instance struct {
	id    *Identifier
	cache cache.Accessor
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

func (i *Instance) netCfg() *networks.DNSConfig {
	return &networks.DNSConfig{
		SearchDomains: []string{
			network.SessionDomain(i.id.SessionID),
			// TODO: append parents
		},
	}
}

func (i *Instance) generateResolv(fp string, dns *networks.DNSConfig) error {
	src, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return err
	}
	defer src.Close()

	override, err := os.Create(fp)
	if err != nil {
		return errors.Wrap(err, "create hosts override")
	}

	defer override.Close()

	return mergeResolv(override, src, dns)
}

func mergeResolv(dst *os.File, src io.Reader, dns *networks.DNSConfig) error {
	srcScan := bufio.NewScanner(src)

	var replacedSearch bool
	var replacedOptions bool

	for _, ns := range dns.Nameservers {
		fmt.Fprintln(dst, "nameserver", ns)
	}

	for srcScan.Scan() {
		switch {
		case strings.HasPrefix(srcScan.Text(), "search"):
			oldDomains := strings.Fields(srcScan.Text())[1:]
			newDomains := append([]string{}, dns.SearchDomains...)
			newDomains = append(newDomains, oldDomains...)
			fmt.Fprintln(dst, "search", strings.Join(newDomains, " "))
			replacedSearch = true
		case strings.HasPrefix(srcScan.Text(), "options"):
			oldOptions := strings.Fields(srcScan.Text())[1:]
			newOptions := append([]string{}, dns.Options...)
			newOptions = append(newOptions, oldOptions...)
			fmt.Fprintln(dst, "options", strings.Join(newOptions, " "))
			replacedOptions = true
		case strings.HasPrefix(srcScan.Text(), "nameserver"):
			if len(dns.Nameservers) == 0 {
				// preserve existing nameservers
				fmt.Fprintln(dst, srcScan.Text())
			}
		default:
			fmt.Fprintln(dst, srcScan.Text())
		}
	}

	if !replacedSearch {
		fmt.Fprintln(dst, "search", strings.Join(dns.SearchDomains, " "))
	}

	if !replacedOptions {
		fmt.Fprintln(dst, "options", strings.Join(dns.Options, " "))
	}

	return nil
}
