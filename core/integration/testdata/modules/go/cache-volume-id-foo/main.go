package main

import "context"

type Foo struct{}

func (*Foo) GetCacheVolumeID(ctx context.Context) (string, error) {
	id, err := dag.CacheVolume("volume-name").ID(ctx)
	return string(id), err
}
