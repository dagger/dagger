package main

import (
	"context"
	"strings"

	"dagger/cli-dev/internal/dagger"
)

type cliS3UploadSet struct {
	Dir   string
	Files []string
}

func (cli *CliDev) publishReleaseArtifactsToS3(
	ctx context.Context,
	dist *dagger.Directory,
	tag string,
	commit string,
	mode cliReleaseMode,
	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion string,
	awsBucket string,
	awsEndpointURL string,
) error {
	sets := []cliS3UploadSet{}
	switch mode {
	case cliReleaseModeMain:
		sets = append(sets,
			cliS3UploadSet{
				Dir:   "dagger/main/" + commit,
				Files: append(cliReleaseArchiveNames(commit), "checksums.txt"),
			},
			cliS3UploadSet{
				Dir:   "dagger/main/head",
				Files: append(cliReleaseArchiveNames("head"), "checksums.txt"),
			},
		)
	case cliReleaseModeStable, cliReleaseModePrerelease:
		sets = append(sets, cliS3UploadSet{
			Dir:   "dagger/releases/" + strings.TrimPrefix(tag, "v"),
			Files: append(cliReleaseArchiveNames(tag), "checksums.txt"),
		})
	}

	var manifest strings.Builder
	for _, set := range sets {
		for _, file := range set.Files {
			manifest.WriteString(set.Dir)
			manifest.WriteByte('\t')
			manifest.WriteString(file)
			manifest.WriteByte('\n')
		}
	}

	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: []string{"aws-cli"},
		}).
		Container().
		WithMountedDirectory("/dist", dist).
		WithNewFile("/upload-manifest", manifest.String()).
		With(optSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID)).
		With(optSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey)).
		WithEnvVariable("AWS_REGION", awsRegion).
		WithEnvVariable("AWS_BUCKET", awsBucket).
		With(optEnvVariable("AWS_ENDPOINT_URL", awsEndpointURL)).
		WithEnvVariable("AWS_EC2_METADATA_DISABLED", "true").
		WithExec([]string{"sh", "-ec", s3UploadScript})

	_, err := ctr.Sync(ctx)
	return err
}

const s3UploadScript = `set -eu
while IFS='	' read -r dir file; do
	[ -n "$dir" ] || continue
	aws_args=""
	if [ -n "${AWS_ENDPOINT_URL:-}" ]; then
		aws_args="--endpoint-url $AWS_ENDPOINT_URL"
	fi
	# shellcheck disable=SC2086
	aws $aws_args s3 cp "/dist/$file" "s3://$AWS_BUCKET/$dir/$file" \
		--content-disposition "attachment;filename=$file"
done < /upload-manifest
`
