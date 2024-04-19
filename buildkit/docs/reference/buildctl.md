# buildctl

`buildctl` is the command-line interface to `buildkitd`.

<!---GENERATE_START buildctl --help-->
```
NAME:
   buildctl - build utility

USAGE:
   buildctl [global options] command [command options] [arguments...]

VERSION:
   v0.0.0+unknown

COMMANDS:
   du               disk usage
   prune            clean up build cache
   prune-histories  clean up build histories
   build, b         build
   debug            debug utilities
   help, h          Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug                enable debug output in logs
   --addr value           buildkitd address (default: "unix:///run/buildkit/buildkitd.sock")
   --log-format value     log formatter: json or text (default: "text")
   --tlsservername value  buildkitd server name for certificate validation
   --tlscacert value      CA certificate for validation
   --tlscert value        client certificate
   --tlskey value         client key
   --tlsdir value         directory containing CA certificate, client certificate, and client key
   --timeout value        timeout backend connection after value seconds (default: 5)
   --wait                 block RPCs until the connection becomes available
   --help, -h             show help
   --version, -v          print the version
```
<!---GENERATE_END-->

## Connecting

`buildctl` connects to a running `buildkitd` instance. The connection can is in a URL format of `<protocol>://<address>`.
Supported `<protocol>` is any supported by [net.Dialer.DialContext()](https://pkg.go.dev/net#Dialer.DialContext).
Practically, that normally will be one of:

* Unix-domain socket via `unix://path/to/socket`, e.g. `unix:///run/buildkit/buildkitd.sock` (which is the default)
* TCP socket via `tcp://<ipaddress>:<port>`, e.g. `tcp://10.0.0.1:2555`

## `build`

Synopsis:

<!---GENERATE_START buildctl build --help-->
```
NAME:
   buildctl build - build

USAGE:
   
  To build and push an image using Dockerfile:
    $ buildctl build --frontend dockerfile.v0 --opt target=foo --opt build-arg:foo=bar --local context=. --local dockerfile=. --output type=image,name=docker.io/username/image,push=true
  

OPTIONS:
   --output value, -o value          Define exports for build result, e.g. --output type=image,name=docker.io/username/image,push=true
   --progress value                  Set type of progress (auto, plain, tty, rawjson). Use plain to show container output (default: "auto")
   --trace value                     Path to trace file. Defaults to no tracing.
   --local value                     Allow build access to the local directory
   --oci-layout value                Allow build access to the local OCI layout
   --frontend value                  Define frontend used for build
   --opt value                       Define custom options for frontend, e.g. --opt target=foo --opt build-arg:foo=bar
   --no-cache                        Disable cache for all the vertices
   --export-cache value              Export build cache, e.g. --export-cache type=registry,ref=example.com/foo/bar, or --export-cache type=local,dest=path/to/dir
   --import-cache value              Import build cache, e.g. --import-cache type=registry,ref=example.com/foo/bar, or --import-cache type=local,src=path/to/dir
   --secret value                    Secret value exposed to the build. Format id=secretname,src=filepath
   --allow value                     Allow extra privileged entitlement, e.g. network.host, security.insecure
   --ssh value                       Allow forwarding SSH agent to the builder. Format default|<id>[=<socket>|<key>[,<key>]]
   --metadata-file value             Output build metadata (e.g., image digest) to a file as JSON
   --source-policy-file value        Read source policy file from a JSON file
   --ref-file value                  Write build ref to a file
   --registry-auth-tlscontext value  Overwrite TLS configuration when authenticating with registries, e.g. --registry-auth-tlscontext host=https://myserver:2376,insecure=false,ca=/path/to/my/ca.crt,cert=/path/to/my/cert.crt,key=/path/to/my/key.crt
   --debug-json-cache-metrics value  Where to output json cache metrics, use 'stdout' or 'stderr' for standard (error) output.
   
```
<!---GENERATE_END-->

`buildctl build` uses a buildkit daemon `buildkitd` to drive a build.

The build consists of the following key elements:

* [frontend definition](#frontend): parses the build descriptor, e.g. dockerfile
* [local sources](#local-sources): sets relevant directories and files passed to the build
* [frontend options](#frontend-options): options that are relevant to the particular frontend
* [output](#output): defines what format of output to use and where to place it
* [cache](#cache): defines where to export the cache generated during the build to, or where to import from

### frontend

The frontend is declared by the flag `--frontend <frontend>`. The `<frontend>` must be one built into `buildkitd`, or an OCI
image that implements the frontend API.

In the above example, we are using the built-in `dockerfile.v0` frontend, which knows how to parse a dockerfile and convert it to LLB.

There currently are two options for `--frontend`:

* `dockerfile.v0`: uses the dockerfile-to-LLB frontend converter that is built into buildkitd.
* `gateway.v0`: uses any OCI image that implements the front-end API, with the image provided by `--opt source=<image>`.

### local sources

A build may need access to various sources local to the `buildctl` execution environment,
such as files and directories or OCI images. These can be provided from the local
environment to which the user of `buildctl` has access. These are provided as:

* `--local <name>=<dir>` - allow buildkitd to access a local-to-buildctl directory `<dir>` under the unique name `<name>`.
* `--oci-layout <name>=<dir>` - allow buildkitd to access OCI images in the local-to-buildctl directory `<dir>` under the unique name `<name>`.

Each of the above is expected to provide a unique name, for this invocation of `buildctl`, for a directory. Other parts of `buildctl` can then
use those "named contexts" to reference directories, files or OCI images.

For example:

```
buildctl build --local test1=/var/lib/test1
```

lets `buildkitd` access all of the files in `/var/lib/test1` (relative to wherever `buildctl` is running), referenced via the name `test1`.

Similarly:

```
buildctl build --oci-layout foo=/var/lib/oci
```

lets `buildkitd` access OCI images under `/var/lib/oci` (relative to wherever `buildctl` is running), referenced via the name `foo`.

These "named references" are used by the frontend, either directly or with explicit options.

#### dockerfile frontend sources

The dockerfile frontend, enabled via `buildctl build --frontend=dockerfile.v0`, expects to have access to 2 named references:

* `context`: where to perform the build.
* `dockerfile`: where to find the dockerfile to parse describing the build.

Thus, a dockerfile build invocation would include:

```
buildctl build --frontend dockerfile.v0 --local context=. --local dockerfile=.
```

The above means, "build using the dockerfile frontend, passing it the context of the current directory where I am running `buildctl`, and the
dockerfile in the current directory as well."

### frontend options

Frontend-specific options are defined via `--opt <key>=<value>`. The specific meanings of those are frontend-specific.

#### dockerfile-specific options

In the above example, we define two:

* `--opt target=foo` - build only until the dockerfile target stage `foo`, the equivalent of `docker buildx build --target=foo`.
* `--opt build-arg:foo=bar` - set the build argument `foo` to `bar`.

In addition, the dockerfile front-end supports additional build contexts. These allow you to "alias" an image reference or name
with something else entirely.

To use the build contexts, pass `--opt context:<source>=<target>`, where the `<source>` is the name in the dockerfile,
and `<target>` is a properly formatted target. These can be the following:

* `--opt context:alpine=local:foo1` - replace usage of `alpine` with a named context `foo1`, that already should have been loaded via `--local`.
* `--opt context:alpine=oci-layout://foo2@sha256:bd04a5b26dec16579cd1d7322e949c5905c4742269663fcbc84dcb2e9f4592fb` - replace usage of `alpine` with the image or index whose sha256 hash is `bd04a5b26dec16579cd1d7322e949c5905c4742269663fcbc84dcb2e9f4592fb` from an OCI layout whose named context `foo2`, that already should have been loaded via `--oci-layout`.
* `--opt context:alpine=docker-image://docker.io/library/ubuntu:latest` - replace usage of `alpine` with the docker image `docker.io/library/ubuntu:latest` from the registry.
* `--opt context:alpine=https://example.com/foo/bar.git` - replace usage of alpine with the contents of the git repository at `https://example.com/foo/bar.git`

Complete examples of using local and OCI layout:

```sh
$ buildctl build --frontend dockerfile.v0 --local context=. --local dockerfile=. --local foo1=/home/dir/abc --opt context:alpine=local:foo1
$ buildctl build --frontend dockerfile.v0 --local context=. --local dockerfile=. --oci-layout foo2=/home/dir/oci --opt context:alpine=oci-layout://foo2@sha256:bd04a5b26dec16579cd1d7322e949c5905c4742269663fcbc84dcb2e9f4592fb
```

#### gateway-specific options

The `gateway.v0` frontend passes all of its `--opt` options on to the OCI image that is called to convert the
input to LLB. The one required option is `--opt source=<image>`, which defines the OCI image to use to convert
the input to LLB.

For example:

```
buildctl build \
    --frontend gateway.v0 \
    --opt source=docker/dockerfile \
    --local context=. \
    --local dockerfile=.
```

Will use `docker/dockerfile` image to convert the Dockerfile input to LLB.

Other `--opt` options are passed to the frontend.

### output

Output defines what to do with the resultant artifact of the build. It should be a series of key=value pairs, comma-separated, the first of
which must be `type=<type>`, where `<type>` is one of the supported types. The result of the options depend on the type.

In our above example:

```
--output type=image,name=docker.io/username/image,push=true
```

* `type=image`: output an OCI image.
* `name=docker.io/username/image`: the name of the image is `docker.io/username/image`.
* `push=true`: attempt to push the generated image to the registry using the `name`

### cache

Cache defines options for buildkit to do one or both of:

* at the end of the build, export additions to cache from the build to external locations
* at the beginning of the build, import artifacts into the cache from external locations for use during the build

#### export cache

During the build process, `buildkitd` generates cache layers. These can be exported at the end of the build via:

```
--export-cache type=<type>,<key>=<value>,...
```

The `<key>` options are defined for the given types.

For example:

* `--export-cache type=registry,ref=example.com/foo/bar` - export the cache to an OCI image.
* `--export-cache type=local,dest=path/to/dir` - export the cache to a directory local to where `buildctl` is running.

#### import cache

During the build process, `buildkitd` uses its local cache to optimize its build. In addition, you
can augment what is in the cache from external locations, i.e. seed the cache.

```
--import-cache type=<type>,<key>=<value>
```

The `<key>` options are defined for the given types, and match those for `--export-cache`.

For example:

* `--import-cache type=registry,ref=example.com/foo/bar` - import into the cache from an OCI image.
* `--import-cache type=local,src=path/to/dir` - import into the cache from a directory local to where `buildctl` is running.
