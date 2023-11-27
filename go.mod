module github.com/dagger/dagger

go 1.21

replace dagger.io/dagger => ./sdk/go

replace github.com/dagger/dagger/internal/mage => ./internal/mage

require (
	dagger.io/dagger v0.9.3
	github.com/99designs/gqlgen v0.17.34 // indirect
	github.com/armon/circbuf v0.0.0-20190214190532-5111143e8da2 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.18.21 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.13.20 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.31.3 // indirect
	github.com/charmbracelet/bubbles v0.16.1
	github.com/charmbracelet/bubbletea v0.24.2
	github.com/containerd/containerd v1.7.2
	github.com/containerd/fuse-overlayfs-snapshotter v1.0.6
	github.com/containerd/stargz-snapshotter v0.14.3
	github.com/containernetworking/cni v1.1.2
	github.com/coreos/go-systemd/v22 v22.5.0
	github.com/dagger/graphql v0.0.0-20231103002502-b36795bcf171
	github.com/dagger/graphql-go-tools v0.0.0-20231012004527-77189e400b6e
	github.com/docker/distribution v2.8.2+incompatible
	github.com/google/go-containerregistry v0.15.2
	github.com/google/uuid v1.4.0
	github.com/iancoleman/strcase v0.3.0
	// https://github.com/moby/buildkit/commit/86e25b3ad8c20fc420669949f24bb86c74082b2f
	github.com/moby/buildkit v0.13.0-beta1.0.20231011161957-86e25b3ad8c2
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0-rc3
	github.com/opencontainers/runtime-spec v1.1.0-rc.2
	github.com/pelletier/go-toml v1.9.5
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.7.0
	github.com/stretchr/testify v1.8.4
	github.com/tonistiigi/fsutil v0.0.0-20230629203738-36ef4d8c0dbb
	github.com/urfave/cli v1.22.12
	github.com/weaveworks/common v0.0.0-20230119144549-0aaa5abd1e63
	github.com/zeebo/xxh3 v1.0.2
	go.etcd.io/bbolt v1.3.7 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.46.0
	go.opentelemetry.io/otel v1.20.0
	go.opentelemetry.io/otel/exporters/jaeger v1.17.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.19.0
	go.opentelemetry.io/otel/sdk v1.20.0
	go.opentelemetry.io/otel/trace v1.20.0
	go.opentelemetry.io/proto/otlp v1.0.0
	golang.org/x/crypto v0.15.0
	golang.org/x/mod v0.13.0
	golang.org/x/sync v0.4.0
	golang.org/x/sys v0.14.0
	golang.org/x/term v0.14.0
	google.golang.org/grpc v1.59.0
	oss.terrastruct.com/d2 v0.4.0
)

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/charmbracelet/lipgloss v0.8.0
	github.com/dagger/dagger/internal/mage v0.0.0-00010101000000-000000000000
	github.com/dave/jennifer v1.7.0
	github.com/dschmidt/go-layerfs v0.1.0
	github.com/go-git/go-git/v5 v5.9.0
	github.com/gogo/protobuf v1.3.2
	github.com/google/go-github/v50 v50.2.0
	github.com/gorilla/websocket v1.5.0
	github.com/icholy/replace v0.6.0
	github.com/jackpal/gateway v1.0.7
	github.com/juju/ansiterm v1.0.0
	github.com/koron-go/prefixw v1.0.0
	github.com/mackerelio/go-osstat v0.2.4
	github.com/mattn/go-isatty v0.0.19
	github.com/mitchellh/go-spdx v0.1.0
	github.com/moby/sys/mount v0.3.3
	github.com/muesli/termenv v0.15.2
	github.com/nxadm/tail v1.4.8
	github.com/opencontainers/runc v1.1.9
	github.com/pkg/browser v0.0.0-20210911075715-681adbf594b8
	github.com/prometheus/procfs v0.12.0
	github.com/psanford/memfs v0.0.0-20230130182539-4dbf7e3e865e
	github.com/rs/cors v1.10.0
	github.com/rs/zerolog v1.30.0
	github.com/shurcooL/graphql v0.0.0-20220606043923-3cf50f8a0a29
	github.com/vito/midterm v0.1.4
	github.com/vito/progrock v0.10.2-0.20230913234310-64b4a1cfb007
	golang.org/x/exp v0.0.0-20231006140011-7918f672742d
	golang.org/x/oauth2 v0.13.0
	golang.org/x/tools v0.14.0
)

require (
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
)

require (
	cdr.dev/slog v1.4.2 // indirect
	dario.cat/mergo v1.0.0 // indirect
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230106234847-43070de90fa1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.0.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v0.4.1 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v0.6.0 // indirect
	github.com/ProtonMail/go-crypto v0.0.0-20230828082145-3c4c8a2d2371 // indirect
	github.com/PuerkitoBio/goquery v1.8.1 // indirect
	github.com/acomagu/bufpipe v1.0.4 // indirect
	github.com/agnivade/levenshtein v1.1.1 // indirect
	github.com/alecthomas/chroma v0.10.0 // indirect
	github.com/alecthomas/chroma/v2 v2.7.0 // indirect
	github.com/anchore/go-struct-converter v0.0.0-20221118182256-c68fdcfa2092 // indirect
	github.com/andybalholm/cascadia v1.3.1 // indirect
	github.com/aws/aws-sdk-go-v2 v1.17.8 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.10 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.11.62 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.32 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.26 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.0.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.26 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.14.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.12.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.14.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.18.9 // indirect
	github.com/aws/smithy-go v1.13.5 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/cloudflare/circl v1.3.3 // indirect
	github.com/containerd/go-cni v1.1.9 // indirect
	github.com/containerd/go-runc v1.1.0 // indirect
	github.com/containerd/typeurl/v2 v2.1.1 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/cyphar/filepath-securejoin v0.2.4 // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/distribution/reference v0.5.0 // indirect
	github.com/dlclark/regexp2 v1.9.0 // indirect
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/dop251/goja v0.0.0-20230402114112-623f9dda9079 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.15.0 // indirect
	github.com/fogleman/ease v0.0.0-20170301025033-8da417bf1776 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.5.0 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/golang-jwt/jwt/v4 v4.4.2 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20230406165453-00490a63f317 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.16.0 // indirect
	github.com/hanwen/go-fuse/v2 v2.2.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.2 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/jonboulle/clockwork v0.4.0 // indirect
	github.com/jung-kurt/gofpdf v1.16.2 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/lunixbochs/vtclean v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mazznoer/csscolorparser v0.1.3 // indirect
	github.com/mitchellh/hashstructure/v2 v2.0.2 // indirect
	github.com/moby/sys/mountinfo v0.6.2 // indirect
	github.com/moby/sys/sequential v0.5.0 // indirect
	github.com/opencontainers/selinux v1.11.0 // indirect
	github.com/package-url/packageurl-go v0.1.1-0.20220428063043-89078438f170 // indirect
	github.com/pjbgf/sha1cd v0.3.0 // indirect
	github.com/pkg/profile v1.5.0 // indirect
	github.com/prometheus/client_golang v1.14.0 // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.42.0 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sergi/go-diff v1.3.1 // indirect
	github.com/skeema/knownhosts v1.2.0 // indirect
	github.com/spdx/tools-golang v0.5.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tonistiigi/go-actions-cache v0.0.0-20220404170428-0bdeb6e1eac7 // indirect
	github.com/tonistiigi/go-archvariant v1.0.0 // indirect
	github.com/tonistiigi/vt100 v0.0.0-20230623042737-f9a4f7ef6531 // indirect
	github.com/weaveworks/promrus v1.2.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/yuin/goldmark v1.5.4 // indirect
	github.com/zmb3/spotify/v2 v2.3.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace v0.45.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.45.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.19.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.19.0 // indirect
	go.opentelemetry.io/otel/metric v1.20.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/image v0.7.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	gonum.org/v1/plot v0.12.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20231016165738-49dd2c1f3d0b // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	oss.terrastruct.com/util-go v0.0.0-20230320053557-dcb5aac7d972 // indirect
)

require (
	github.com/Khan/genqlient v0.6.0
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/Microsoft/hcsshim v0.10.0-rc.8 // indirect
	github.com/adrg/xdg v0.4.0
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/cenkalti/backoff/v4 v4.2.1
	github.com/containerd/cgroups v1.1.0 // indirect
	github.com/containerd/console v1.0.4-0.20230313162750-1ae8d489ac81 // indirect
	github.com/containerd/continuity v0.4.1
	github.com/containerd/fifo v1.1.0 // indirect
	github.com/containerd/nydus-snapshotter v0.8.2 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.14.3 // indirect
	github.com/containerd/ttrpc v1.2.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/cli v24.0.5+incompatible
	github.com/docker/docker v24.0.0-rc.2.0.20230905130451-032797ea4bcb+incompatible
	github.com/docker/docker-credential-helpers v0.7.0 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/felixge/httpsnoop v1.0.3 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gofrs/flock v0.8.1
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/in-toto/in-toto-golang v0.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/compress v1.16.5
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/moby/locker v1.0.1
	github.com/moby/patternmatcher v0.6.0
	github.com/moby/sys/signal v0.7.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.4.0 // indirect
	github.com/shibumi/go-pathspec v1.3.0 // indirect
	github.com/spf13/pflag v1.0.5
	github.com/tidwall/gjson v1.17.0
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea
	github.com/vbatts/tar-split v0.11.3 // indirect
	github.com/vektah/gqlparser/v2 v2.5.6
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/net v0.18.0
	golang.org/x/text v0.14.0
	golang.org/x/time v0.3.0 // indirect
	google.golang.org/genproto v0.0.0-20231030173426-d783a09b4405 // indirect
	google.golang.org/protobuf v1.31.0
	gopkg.in/yaml.v3 v3.0.1
)
