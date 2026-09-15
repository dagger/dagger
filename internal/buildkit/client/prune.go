package client

import (
	"context"
	"io"
	"time"

	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	"github.com/pkg/errors"
)

func (c *Client) Prune(ctx context.Context, ch chan UsageInfo, opts ...PruneOption) error {
	info := &PruneInfo{}
	for _, o := range opts {
		o.SetPruneOption(info)
	}

	req := &controlapi.PruneRequest{
		Filter:        info.Filter,
		KeepDuration:  int64(info.KeepDuration),
		ReservedSpace: int64(info.ReservedSpace),
		TargetSpace:   int64(info.TargetSpace),
		MaxUsedSpace:  int64(info.MaxUsedSpace),
		MinFreeSpace:  int64(info.MinFreeSpace),
	}
	if info.All {
		req.All = true
	}
	cl, err := c.ControlClient().Prune(ctx, req)
	if err != nil {
		return errors.Wrap(err, "failed to call prune")
	}

	for {
		d, err := cl.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if ch != nil {
			ch <- UsageInfo{
				ID:          d.ID,
				Mutable:     d.Mutable,
				InUse:       d.InUse,
				Size:        d.Size_,
				Parents:     d.Parents,
				CreatedAt:   d.CreatedAt,
				Description: d.Description,
				UsageCount:  int(d.UsageCount),
				LastUsedAt:  d.LastUsedAt,
				RecordType:  UsageRecordType(d.RecordType),
				Shared:      d.Shared,
			}
		}
	}
}

type PruneOption interface {
	SetPruneOption(*PruneInfo)
}

type PruneInfo struct {
	All          bool          `json:"all"`
	Filter       []string      `json:"filter"`
	KeepDuration time.Duration `json:"keepDuration"`

	ReservedSpace int64 `json:"reservedSpace"`
	MaxUsedSpace  int64 `json:"maxUsedSpace"`
	TargetSpace   int64 `json:"targetSpace"`
	MinFreeSpace  int64 `json:"minFreeSpace"`
}

type pruneOptionFunc func(*PruneInfo)

func (f pruneOptionFunc) SetPruneOption(pi *PruneInfo) {
	f(pi)
}

var PruneAll = pruneOptionFunc(func(pi *PruneInfo) {
	pi.All = true
})

func WithKeepOpt(duration time.Duration, reserved int64, max int64, free int64) PruneOption {
	return pruneOptionFunc(func(pi *PruneInfo) {
		pi.KeepDuration = duration
		pi.ReservedSpace = reserved
		pi.MaxUsedSpace = max
		pi.MinFreeSpace = free
	})
}
