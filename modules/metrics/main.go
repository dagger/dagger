package main

import (
	"dagger/metrics/internal/dagger"
)

const (
	// https://hub.docker.com/r/grafana/grafana/tags
	grafanaVersion = "11.6.3@sha256:6128afd8174f01e39a78341cb457588f723bbb9c3b25c4d43c4b775881767069"
	// https://hub.docker.com/r/prom/prometheus/tags
	prometheusVersion = "3.4.1@sha256:9abc6cf6aea7710d163dbb28d8eeb7dc5baef01e38fa4cd146a406dd9f07f70d"
)

type Metrics struct {
	// Directory with all config files
	Config *dagger.Directory
}

func New(
	// +defaultPath="./config"
	config *dagger.Directory,
) *Metrics {
	return &Metrics{
		Config: config,
	}
}

// Grafana configured with Prometheus & Dagger Engine metrics
func (m *Metrics) Run() *dagger.Container {
	return m.Grafana().
		WithServiceBinding("prometheus", m.Prometheus().AsService())
}

// Grafana container configured with Prometheus & Dagger Engine metrics
func (m *Metrics) Grafana() *dagger.Container {
	return dag.Container().
		From("grafana/grafana:"+grafanaVersion).
		WithExposedPort(3000).
		WithMountedCache("/var/lib/grafana", dag.CacheVolume("grafana-"+grafanaVersion), dagger.ContainerWithMountedCacheOpts{
			Owner: "grafana",
		}).
		WithMountedDirectory("/var/lib/grafana/dashboards", m.Config.Directory("grafana/dashboards")).
		WithMountedDirectory("/etc/grafana/provisioning", m.Config.Directory("grafana/provisioning"))
}

// Prometheus container configured to scrape Dagger Engine metrics
func (m *Metrics) Prometheus() *dagger.Container {
	devEngineSvc := dag.DaggerEngine().Service("dagger-dev-metrics", dagger.DaggerEngineServiceOpts{
		Metrics: true,
	})

	devEngineLoadTest := dag.DaggerEngine().Container().
		WithFile("/load-test.sh", m.Config.File("load-test.sh"), dagger.ContainerWithFileOpts{
			Permissions: 0755,
		}).
		WithEntrypoint([]string{"/load-test.sh"}).
		WithServiceBinding("dagger", devEngineSvc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger:1234")

	return dag.Container().
		From("prom/prometheus:"+prometheusVersion).
		WithExposedPort(9090).
		WithMountedCache("/prometheus", dag.CacheVolume("prometheus-"+prometheusVersion), dagger.ContainerWithMountedCacheOpts{
			Owner: "nobody",
		}).
		WithFile("/etc/prometheus/prometheus.yml", m.Config.File("prometheus.yml")).
		WithServiceBinding("dagger", devEngineSvc).
		WithServiceBinding("dagger-load-test", devEngineLoadTest.AsService(dagger.ContainerAsServiceOpts{
			UseEntrypoint: true,
		}))
}
