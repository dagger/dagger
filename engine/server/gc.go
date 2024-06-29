package server

import (
	"context"
	"time"

	controlapi "github.com/moby/buildkit/api/services/control"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/imageutil"
	"golang.org/x/sync/errgroup"
)

func (srv *Server) DiskUsage(ctx context.Context, r *controlapi.DiskUsageRequest) (*controlapi.DiskUsageResponse, error) {
	resp := &controlapi.DiskUsageResponse{}
	du, err := srv.baseWorker.DiskUsage(ctx, bkclient.DiskUsageInfo{
		Filter: r.Filter,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range du {
		resp.Record = append(resp.Record, &controlapi.UsageRecord{
			ID:          r.ID,
			Mutable:     r.Mutable,
			InUse:       r.InUse,
			Size_:       r.Size,
			Parents:     r.Parents,
			UsageCount:  int64(r.UsageCount),
			Description: r.Description,
			CreatedAt:   r.CreatedAt,
			LastUsedAt:  r.LastUsedAt,
			RecordType:  string(r.RecordType),
			Shared:      r.Shared,
		})
	}
	return resp, nil
}

func (srv *Server) Prune(req *controlapi.PruneRequest, stream controlapi.Control_PruneServer) error {
	eg, ctx := errgroup.WithContext(stream.Context())

	srv.daggerSessionsMu.RLock()
	cancelLeases := len(srv.daggerSessions) == 0
	srv.daggerSessionsMu.RUnlock()
	if cancelLeases {
		imageutil.CancelCacheLeases()
	}

	didPrune := false
	defer func() {
		if didPrune {
			if e, ok := srv.SolverCache.(interface {
				ReleaseUnreferenced(context.Context) error
			}); ok {
				if err := e.ReleaseUnreferenced(ctx); err != nil {
					bklog.G(ctx).Errorf("failed to release cache metadata: %+v", err)
				}
			}
		}
	}()

	ch := make(chan bkclient.UsageInfo, 32)

	eg.Go(func() error {
		defer close(ch)
		return srv.baseWorker.Prune(ctx, ch, bkclient.PruneInfo{
			Filter:       req.Filter,
			All:          req.All,
			KeepDuration: time.Duration(req.KeepDuration),
			KeepBytes:    req.KeepBytes,
		})
	})

	eg.Go(func() error {
		defer func() {
			// drain channel on error
			for range ch {
			}
		}()
		for r := range ch {
			didPrune = true
			if err := stream.Send(&controlapi.UsageRecord{
				ID:          r.ID,
				Mutable:     r.Mutable,
				InUse:       r.InUse,
				Size_:       r.Size,
				Parents:     r.Parents,
				UsageCount:  int64(r.UsageCount),
				Description: r.Description,
				CreatedAt:   r.CreatedAt,
				LastUsedAt:  r.LastUsedAt,
				RecordType:  string(r.RecordType),
				Shared:      r.Shared,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	return eg.Wait()
}

func (srv *Server) gc() {
	srv.gcmu.Lock()
	defer srv.gcmu.Unlock()

	ch := make(chan bkclient.UsageInfo)
	eg, ctx := errgroup.WithContext(context.TODO())

	var size int64
	eg.Go(func() error {
		for ui := range ch {
			size += ui.Size
		}
		return nil
	})

	eg.Go(func() error {
		defer close(ch)
		if policy := srv.baseWorker.GCPolicy(); len(policy) > 0 {
			return srv.baseWorker.Prune(ctx, ch, policy...)
		}
		return nil
	})

	err := eg.Wait()
	if err != nil {
		bklog.G(ctx).Errorf("gc error: %+v", err)
	}
	if size > 0 {
		bklog.G(ctx).Debugf("gc cleaned up %d bytes", size)
	}
}

func getGCPolicy(cfg config.GCConfig, root string) []bkclient.PruneInfo {
	if cfg.GC != nil && !*cfg.GC {
		return nil
	}
	if len(cfg.GCPolicy) == 0 {
		cfg.GCPolicy = defaultGCPolicy(cfg.GCKeepStorage)
	}
	out := make([]bkclient.PruneInfo, 0, len(cfg.GCPolicy))
	for _, rule := range cfg.GCPolicy {
		out = append(out, bkclient.PruneInfo{
			Filter:       rule.Filters,
			All:          rule.All,
			KeepBytes:    rule.KeepBytes.AsBytes(root),
			KeepDuration: rule.KeepDuration.Duration,
		})
	}
	return out
}

func defaultGCPolicy(keep config.DiskSpace) []config.GCPolicy {
	if keep == (config.DiskSpace{}) {
		keep = config.DiskSpace{Percentage: DiskSpacePercentage}
	}
	return []config.GCPolicy{
		// if build cache uses more than 512MB delete the most easily reproducible data after it has not been used for 2 days
		{
			Filters:      []string{"type==source.local,type==exec.cachemount,type==source.git.checkout"},
			KeepDuration: config.Duration{Duration: time.Duration(48) * time.Hour}, // 48h
			KeepBytes:    config.DiskSpace{Bytes: 512 * 1e6},                       // 512MB
		},
		// remove any data not used for 60 days
		{
			KeepDuration: config.Duration{Duration: time.Duration(60) * 24 * time.Hour}, // 60d
			KeepBytes:    keep,
		},
		// keep the unshared build cache under cap
		{
			KeepBytes: keep,
		},
		// if previous policies were insufficient start deleting internal data to keep build cache under cap
		{
			All:       true,
			KeepBytes: keep,
		},
	}
}

const DiskSpacePercentage int64 = 75
