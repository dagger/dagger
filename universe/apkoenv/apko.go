package apkoenv

import (
	"runtime"

	"dagger.io/dagger"
	"gopkg.in/yaml.v3"
)

type ApkoOpts struct {
	Repositories []string
}

type cfg map[string]any

var baseConfig = cfg{
	"cmd": "/bin/sh",
	"environment": cfg{
		"PATH": "/usr/sbin:/sbin:/usr/bin:/bin",
	},
	"archs": []string{runtime.GOARCH},
}

func Alpine(ctx dagger.Context, packages []string, opts_ ...ApkoOpts) *dagger.Container {
	ic := baseConfig
	ic["contents"] = cfg{
		"repositories": []string{
			"https://dl-cdn.alpinelinux.org/alpine/edge/main",
		},
		"packages": append([]string{"alpine-base"}, packages...),
	}
	return apko(ctx, ic)
}

func Wolfi(ctx dagger.Context, packages []string, opts_ ...ApkoOpts) *dagger.Container {
	ic := baseConfig
	ic["contents"] = cfg{
		"repositories": []string{
			"https://packages.wolfi.dev/os",
		},
		"keyring": []string{
			"https://packages.wolfi.dev/os/wolfi-signing.rsa.pub",
		},
		"packages": append([]string{"wolfi-base"}, packages...),
	}
	return apko(ctx, ic)
}

func apko(ctx dagger.Context, ic any) *dagger.Container {
	dag := ctx.Client()

	config, err := yaml.Marshal(ic)
	if err != nil {
		panic(err)
	}

	return dag.Container().Import(
		dag.Container().
			From("cgr.dev/chainguard/apko").
			WithMountedFile(
				"/config.yml",
				dag.Directory().
					WithNewFile("config.yml", string(config)).
					File("config.yml"),
			).
			WithDirectory("/layout", dag.Directory()).
			WithMountedCache("/apkache", dag.CacheVolume("apko")).
			WithExec([]string{
				"build",
				"--cache-dir", "/apkache",
				"/config.yml", "latest", "/layout.tar",
			}).
			File("/layout.tar"),
	)
}
