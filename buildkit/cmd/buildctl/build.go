package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/containerd/continuity"
	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/cmd/buildctl/build"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/progress/progresswriter"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var buildCommand = cli.Command{
	Name:    "build",
	Aliases: []string{"b"},
	Usage:   "build",
	UsageText: `
	To build and push an image using Dockerfile:
	  $ buildctl build --frontend dockerfile.v0 --opt target=foo --opt build-arg:foo=bar --local context=. --local dockerfile=. --output type=image,name=docker.io/username/image,push=true
	`,
	Action: buildAction,
	Flags: []cli.Flag{
		cli.StringSliceFlag{
			Name:  "output,o",
			Usage: "Define exports for build result, e.g. --output type=image,name=docker.io/username/image,push=true",
		},
		cli.StringFlag{
			Name:  "progress",
			Usage: "Set type of progress (auto, plain, tty, rawjson). Use plain to show container output",
			Value: "auto",
		},
		cli.StringFlag{
			Name:  "trace",
			Usage: "Path to trace file. Defaults to no tracing.",
		},
		cli.StringSliceFlag{
			Name:  "local",
			Usage: "Allow build access to the local directory",
		},
		cli.StringSliceFlag{
			Name:  "oci-layout",
			Usage: "Allow build access to the local OCI layout",
		},
		cli.StringFlag{
			Name:  "frontend",
			Usage: "Define frontend used for build",
		},
		cli.StringSliceFlag{
			Name:  "opt",
			Usage: "Define custom options for frontend, e.g. --opt target=foo --opt build-arg:foo=bar",
		},
		cli.BoolFlag{
			Name:  "no-cache",
			Usage: "Disable cache for all the vertices",
		},
		cli.StringSliceFlag{
			Name:  "export-cache",
			Usage: "Export build cache, e.g. --export-cache type=registry,ref=example.com/foo/bar, or --export-cache type=local,dest=path/to/dir",
		},
		cli.StringSliceFlag{
			Name:  "import-cache",
			Usage: "Import build cache, e.g. --import-cache type=registry,ref=example.com/foo/bar, or --import-cache type=local,src=path/to/dir",
		},
		cli.StringSliceFlag{
			Name:  "secret",
			Usage: "Secret value exposed to the build. Format id=secretname,src=filepath",
		},
		cli.StringSliceFlag{
			Name:  "allow",
			Usage: "Allow extra privileged entitlement, e.g. network.host, security.insecure",
		},
		cli.StringSliceFlag{
			Name:  "ssh",
			Usage: "Allow forwarding SSH agent to the builder. Format default|<id>[=<socket>|<key>[,<key>]]",
		},
		cli.StringFlag{
			Name:  "metadata-file",
			Usage: "Output build metadata (e.g., image digest) to a file as JSON",
		},
		cli.StringFlag{
			Name:  "source-policy-file",
			Usage: "Read source policy file from a JSON file",
		},
		cli.StringFlag{
			Name:  "ref-file",
			Usage: "Write build ref to a file",
		},
		cli.StringSliceFlag{
			Name:  "registry-auth-tlscontext",
			Usage: "Overwrite TLS configuration when authenticating with registries, e.g. --registry-auth-tlscontext host=https://myserver:2376,insecure=false,ca=/path/to/my/ca.crt,cert=/path/to/my/cert.crt,key=/path/to/my/key.crt",
		},
		cli.StringFlag{
			Name:  "debug-json-cache-metrics",
			Usage: "Where to output json cache metrics, use 'stdout' or 'stderr' for standard (error) output.",
		},
	},
}

func read(r io.Reader, clicontext *cli.Context) (*llb.Definition, error) {
	def, err := llb.ReadFrom(r)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse input")
	}
	if clicontext.Bool("no-cache") {
		for _, dt := range def.Def {
			var op pb.Op
			if err := (&op).Unmarshal(dt); err != nil {
				return nil, errors.Wrap(err, "failed to parse llb proto op")
			}
			dgst := digest.FromBytes(dt)
			opMetadata, ok := def.Metadata[dgst]
			if !ok {
				opMetadata = pb.OpMetadata{}
			}
			c := llb.Constraints{Metadata: opMetadata}
			llb.IgnoreCache(&c)
			def.Metadata[dgst] = c.Metadata
		}
	}
	return def, nil
}

func openTraceFile(clicontext *cli.Context) (*os.File, error) {
	if traceFileName := clicontext.String("trace"); traceFileName != "" {
		return os.OpenFile(traceFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	}
	return nil, nil
}

func openCacheMetricsFile(clicontext *cli.Context) (*os.File, error) {
	switch out := clicontext.String("debug-json-cache-metrics"); out {
	case "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	case "":
		return nil, nil
	default:
		return os.OpenFile(out, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	}
}

func buildAction(clicontext *cli.Context) error {
	startTime := time.Now()
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	traceFile, err := openTraceFile(clicontext)
	if err != nil {
		return err
	}
	cacheMetricsFile, err := openCacheMetricsFile(clicontext)
	if err != nil {
		return err
	}

	var traceEnc *json.Encoder
	if traceFile != nil {
		defer traceFile.Close()
		traceEnc = json.NewEncoder(traceFile)

		bklog.L.Infof("tracing logs to %s", traceFile.Name())
	}

	dockerConfig := config.LoadDefaultConfigFile(os.Stderr)
	tlsConfigs, err := build.ParseRegistryAuthTLSContext(clicontext.StringSlice("registry-auth-tlscontext"))
	if err != nil {
		return err
	}
	attachable := []session.Attachable{authprovider.NewDockerAuthProvider(dockerConfig, tlsConfigs)}

	if ssh := clicontext.StringSlice("ssh"); len(ssh) > 0 {
		configs, err := build.ParseSSH(ssh)
		if err != nil {
			return err
		}
		sp, err := sshprovider.NewSSHAgentProvider(configs)
		if err != nil {
			return err
		}
		attachable = append(attachable, sp)
	}

	if secrets := clicontext.StringSlice("secret"); len(secrets) > 0 {
		secretProvider, err := build.ParseSecret(secrets)
		if err != nil {
			return err
		}
		attachable = append(attachable, secretProvider)
	}

	allowed, err := build.ParseAllow(clicontext.StringSlice("allow"))
	if err != nil {
		return err
	}

	exports, err := build.ParseOutput(clicontext.StringSlice("output"))
	if err != nil {
		return err
	}

	cacheExports, err := build.ParseExportCache(clicontext.StringSlice("export-cache"))
	if err != nil {
		return err
	}
	cacheImports, err := build.ParseImportCache(clicontext.StringSlice("import-cache"))
	if err != nil {
		return err
	}

	var srcPol *spb.Policy
	if srcPolFile := clicontext.String("source-policy-file"); srcPolFile != "" {
		b, err := os.ReadFile(srcPolFile)
		if err != nil {
			return err
		}
		var srcPolStruct spb.Policy
		if err := json.Unmarshal(b, &srcPolStruct); err != nil {
			return errors.Wrapf(err, "failed to unmarshal source-policy-file %q", srcPolFile)
		}
		srcPol = &srcPolStruct
	}

	eg, ctx := errgroup.WithContext(bccommon.CommandContext(clicontext))

	ref := identity.NewID()

	solveOpt := client.SolveOpt{
		Exports: exports,
		// LocalMounts is set later
		Frontend: clicontext.String("frontend"),
		// FrontendAttrs is set later
		// OCILayouts is set later
		CacheExports:        cacheExports,
		CacheImports:        cacheImports,
		Session:             attachable,
		AllowedEntitlements: allowed,
		SourcePolicy:        srcPol,
		Ref:                 ref,
	}

	solveOpt.FrontendAttrs, err = build.ParseOpt(clicontext.StringSlice("opt"))
	if err != nil {
		return errors.Wrap(err, "invalid opt")
	}

	solveOpt.LocalMounts, err = build.ParseLocal(clicontext.StringSlice("local"))
	if err != nil {
		return errors.Wrap(err, "invalid local")
	}

	solveOpt.OCIStores, err = build.ParseOCILayout(clicontext.StringSlice("oci-layout"))
	if err != nil {
		return errors.Wrap(err, "invalid oci-layout")
	}

	var def *llb.Definition
	if clicontext.String("frontend") == "" {
		if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
			return errors.Errorf("please specify --frontend or pipe LLB definition to stdin")
		}
		def, err = read(os.Stdin, clicontext)
		if err != nil {
			return err
		}
		if len(def.Def) == 0 {
			return errors.Errorf("empty definition sent to build. Specify --frontend instead?")
		}
	} else {
		if clicontext.Bool("no-cache") {
			solveOpt.FrontendAttrs["no-cache"] = ""
		}
	}

	refFile := clicontext.String("ref-file")
	if refFile != "" {
		defer func() {
			continuity.AtomicWriteFile(refFile, []byte(ref), 0666)
		}()
	}

	// not using shared context to not disrupt display but let is finish reporting errors
	pw, err := progresswriter.NewPrinter(context.TODO(), os.Stderr, clicontext.String("progress"))
	if err != nil {
		return err
	}

	if traceEnc != nil {
		traceCh := make(chan *client.SolveStatus)
		pw = progresswriter.Tee(pw, traceCh)
		eg.Go(func() error {
			for s := range traceCh {
				if err := traceEnc.Encode(s); err != nil {
					return err
				}
			}
			return nil
		})
	}
	meg, ctx := errgroup.WithContext(bccommon.CommandContext(clicontext))
	if cacheMetricsFile != nil {
		bklog.L.Infof("writing JSON cache metrics to %s", cacheMetricsFile.Name())
		metricsCh := make(chan *client.SolveStatus)
		pw = progresswriter.Tee(pw, metricsCh)
		meg.Go(func() error {
			vtxMap := tailVTXInfo(ctx, pw, metricsCh)
			if cacheMetricsFile == os.Stdout || cacheMetricsFile == os.Stdin {
				// make sure everything was printed out to get it as the last line.
				eg.Wait()
			} else {
				defer cacheMetricsFile.Close()
			}
			outputCacheMetrics(cacheMetricsFile, startTime, vtxMap)
			return nil
		})
	}
	mw := progresswriter.NewMultiWriter(pw)

	var writers []progresswriter.Writer
	for _, at := range attachable {
		if s, ok := at.(interface {
			SetLogger(progresswriter.Logger)
		}); ok {
			w := mw.WithPrefix("", false)
			s.SetLogger(func(s *client.SolveStatus) {
				w.Status() <- s
			})
			writers = append(writers, w)
		}
	}

	var subMetadata map[string][]byte

	eg.Go(func() error {
		defer func() {
			for _, w := range writers {
				close(w.Status())
			}
		}()

		sreq := gateway.SolveRequest{
			Frontend:    solveOpt.Frontend,
			FrontendOpt: solveOpt.FrontendAttrs,
		}
		if def != nil {
			sreq.Definition = def.ToPB()
		}
		resp, err := c.Build(ctx, solveOpt, "buildctl", func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
			_, isSubRequest := sreq.FrontendOpt["requestid"]
			if isSubRequest {
				if _, ok := sreq.FrontendOpt["frontend.caps"]; !ok {
					sreq.FrontendOpt["frontend.caps"] = "moby.buildkit.frontend.subrequests"
				}
			}
			res, err := c.Solve(ctx, sreq)
			if err != nil {
				return nil, err
			}
			if isSubRequest && res != nil {
				subMetadata = res.Metadata
			}
			return res, err
		}, progresswriter.ResetTime(mw.WithPrefix("", false)).Status())
		if err != nil {
			return err
		}
		for k, v := range resp.ExporterResponse {
			bklog.G(ctx).Debugf("exporter response: %s=%s", k, v)
		}

		metadataFile := clicontext.String("metadata-file")
		if metadataFile != "" && resp.ExporterResponse != nil {
			if err := writeMetadataFile(metadataFile, resp.ExporterResponse); err != nil {
				return err
			}
		}

		return nil
	})

	eg.Go(func() error {
		<-pw.Done()
		return pw.Err()
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	if txt, ok := subMetadata["result.txt"]; ok {
		fmt.Print(string(txt))
	} else {
		for k, v := range subMetadata {
			if strings.HasPrefix(k, "result.") {
				fmt.Printf("%s\n%s\n", k, v)
			}
		}
	}

	meg.Wait()

	return nil
}

func writeMetadataFile(filename string, exporterResponse map[string]string) error {
	var err error
	out := make(map[string]interface{})
	for k, v := range exporterResponse {
		dt, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			out[k] = v
			continue
		}
		var raw map[string]interface{}
		if err = json.Unmarshal(dt, &raw); err != nil || len(raw) == 0 {
			out[k] = v
			continue
		}
		out[k] = json.RawMessage(dt)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return continuity.AtomicWriteFile(filename, b, 0666)
}
