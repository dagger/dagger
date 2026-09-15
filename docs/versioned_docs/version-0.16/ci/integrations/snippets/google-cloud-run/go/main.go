package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Deploy(ctx context.Context, projectName, serviceLocation, imageAddress string, servicePort int, credential *dagger.Secret) (string, error) {
	addr, err := dag.GoogleCloudRun().CreateService(ctx, projectName, serviceLocation, imageAddress, servicePort, credential)
	if err != nil {
		return "", err
	}
	return addr, nil
}
