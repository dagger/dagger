---
slug: /13ec8/dagger-env-reference
displayed_sidebar: '0.2'
---

# Dagger Environment Variables Reference

Dagger supports a variety of environment variables.

They have two purposes:

- Extend Dagger capabilities
- Persist CLI options

## Extend Dagger capabilities

Dagger aims to fit any use case, even the most specific ones. To avoid burdening the user experience with rarely used options, a small list of environment variables can be set:

| Environment variable            | Source                                                                                                                                  | Description                                               |
| :------------------------------ | :-------------------------------------------------------------------------------------------------------------------------------------- | :-------------------------------------------------------- |
| `BUILDKIT_HOST`                 | [custom buildkit](https://docs.dagger.io/1223/custom-buildkit/#using-a-custom-buildkit-daemon)                                          | Link Dagger to custom Buildkit instance                   |
| `DOCKER_HOST`                   | [custom buildkit w/Docker](https://docs.dagger.io/1223/custom-buildkit/#using-a-custom-remote-buildkit-running-in-docker)               | Link Dagger to custom Buildkit instance running in Docker |
| `OTEL_EXPORTER_JAEGER_ENDPOINT` | [opentelemetry](https://docs.dagger.io/1223/custom-buildkit/#opentelemetry-support)                                                     | Opentelemetry Jaeger endpoint                             |
| `JAEGER_TRACE`                  | [opentelemetry](https://docs.dagger.io/1223/custom-buildkit/#opentelemetry-support)                                                     | Jaeger UI endpoint                                        |
| `DOCKERHUB_AUTH_USER`           | [image.cue](https://github.com/dagger/dagger/blob/3709d3da00325d410721014c401b82cbcc1116c9/pkg/dagger.io/dagger/core/image.cue#L74-L79) | `Docker.#Pull` repository login                           |
| `DOCKERHUB_AUTH_PASSWORD`       | [image.cue](https://github.com/dagger/dagger/blob/3709d3da00325d410721014c401b82cbcc1116c9/pkg/dagger.io/dagger/core/image.cue#L74-L79) | `Docker.#Pull` repository password                        |

## Persist CLI options

As Dagger relies on Viper to manage the CLI inputs, all its option can be replaced with environment variables:

### dagger version

| option    | Usage                   | Description                   |
| :-------- | ----------------------- | ----------------------------- |
| `--check` | export `DAGGER_CHECK=1` | check if dagger is up to date |

### dagger project

#### init

| option       | Usage                           | Description           |
| :----------- | ------------------------------- | --------------------- |
| `--name`     | export `DAGGER_NAME=string`     | project name          |
| `--template` | export `DAGGER_TEMPLATE=string` | Template name [hello] |

#### update

| option                   | Usage                                       | Description                                    |
| :----------------------- | ------------------------------------------- | ---------------------------------------------- |
| `--private-key-file`     | export `DAGGER_PRIVATE_KEY_FILE=string`     | Private ssh key                                |
| `--private-key-password` | export `DAGGER_PRIVATE_KEY_PASSWORD=string` | Private ssh key password                       |
| `--update`               | export `DAGGER_UPDATE=1`                    | Update to latest version of specified packages |

### dagger do

| option            | Usage                                | Description                                                              |
| :---------------- | ------------------------------------ | ------------------------------------------------------------------------ |
| `--cache-from`    | export `DAGGER_CACHE_FROM=string`    | External cache sources (eg. user/app:cache, type=local,src=path/to/dir)  |
| `--cache-to`      | export `DAGGER_CACHE_TO=string`      | Cache destinations (eg. user/app:cache, type=local,dest=path/to/dir)     |
| `--dry-run`       | export `DAGGER_DRY_RUN=1`            | Dry run mode                                                             |
| `--no-cache`      | export `DAGGER_NO_CACHE=1`           | Disable caching                                                          |
| `--output`        | export `DAGGER_OUTPUT=string`        | File path to write the action's output values. Prints to stdout if empty |
| `--output-format` | export `DAGGER_OUTPUT_FORMAT=string` | Format for output values (plain, json, yaml)                             |
| `--plan`          | export `DAGGER_PLAN=string`          | Path to plan (defaults to current directory) (default ".")               |
| `--platform`      | export `DAGGER_PLATFORM=string`      | Set target build platform (requires experimental)                        |
| `--with`          | export `DAGGER_WITH=string`          | Set value of dagger value at runtime                                     |
