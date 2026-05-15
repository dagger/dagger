package main

import "context"

type Foo struct{}

func (*Foo) UseCacheVolume(ctx context.Context) (string, error) {
	id, err := dag.CacheVolume("cache-name").ID(ctx)
	return string(id), err
}

func (f *Foo) PassCacheVolume(ctx context.Context) (string, error) {
	return f.UseCacheVolume(ctx)
}
