package universe

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

func Alpine(ctx Context, packages []string, opts_ ...ApkoOpts) *dagger.Container {
	ic := baseConfig
	ic["contents"] = cfg{
		"repositories": []string{
			"https://dl-cdn.alpinelinux.org/alpine/edge/main",
		},
		"packages": append([]string{"alpine-base"}, packages...),
	}
	return apko(ctx, ic)
}

func Wolfi(ctx Context, packages []string, opts_ ...ApkoOpts) *dagger.Container {
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

func apko(ctx Context, ic any) *dagger.Container {
	config, err := yaml.Marshal(ic)
	if err != nil {
		panic(err)
	}

	configDir := ctx.Client().Directory().
		WithNewFile("config.yml", string(config))

	fork := ctx.Client().
		Git("https://github.com/vito/apko").
		Branch("oci-layout").
		Tree()

	// TODO: until layout dir support is merged upstream:
	// https://github.com/chainguard-dev/apko/pull/769
	apko := ctx.Client().
		Container().
		From("golang:alpine").
		With(GoCache(ctx)).
		WithMountedDirectory("/apko", fork).
		WithExec([]string{"go", "install", "-C", "/apko", "."}).
		WithEntrypoint([]string{"apko"})

	layout := apko.
		WithMountedFile("/config.yml", configDir.File("config.yml")).
		WithDirectory("/layout", ctx.Client().Directory()).
		WithMountedCache("/apkache", ctx.Client().CacheVolume("apko")).
		WithFocus().
		WithExec([]string{
			"build",
			"--debug",
			"--cache-dir", "/apkache",
			"/config.yml",
			"latest",
			"/layout",
		}).
		Directory("/layout")

	return ctx.Client().Container().ImportDir(layout)
}
