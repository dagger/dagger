package main

import "context"

type Bar struct{}

func (*Bar) GetCacheVolumeID(ctx context.Context) (string, error) {
	id, err := dag.CacheVolume("volume-name").ID(ctx)
	return string(id), err
}
