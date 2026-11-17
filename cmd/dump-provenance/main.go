// Command dump-provenance collects public artifact provenance from an engine.
// Saved reports can be inspected with the local selector matcher.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	dagger "dagger.io/dagger"
)

type pathsFlag []string

func (p *pathsFlag) String() string { return fmt.Sprint([]string(*p)) }
func (p *pathsFlag) Set(value string) error {
	*p = append(*p, value)
	return nil
}

type options struct {
	workspace, workdir, artifactType, uri, input string
	files, paths                                 pathsFlag
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "dump-provenance:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var opts options
	flags := flag.NewFlagSet("dump-provenance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.workspace, "workspace", "", "workspace reference for a new engine session")
	flags.StringVar(&opts.workdir, "workdir", "", "working directory for a new engine session")
	flags.StringVar(&opts.artifactType, "type", "Check", "artifact type; an empty value selects all types")
	flags.StringVar(&opts.uri, "uri", "", "public artifact URI filter")
	flags.StringVar(&opts.input, "input", "", "read a saved report instead of connecting; '-' reads stdin")
	flags.Var(&opts.files, "match-file", "match a workspace-root file locally; can be repeated")
	flags.Var(&opts.paths, "match-path", "match a workspace-root subtree locally; can be repeated")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	var result *report
	if opts.input != "" {
		if opts.workspace != "" || opts.workdir != "" || opts.uri != "" {
			return fmt.Errorf("-workspace, -workdir, and -uri require engine-backed collection")
		}
		explicitType := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "type" {
				explicitType = true
			}
		})
		if explicitType {
			return fmt.Errorf("-type requires engine-backed collection")
		}
		reader := stdin
		if opts.input != "-" {
			file, err := os.Open(opts.input)
			if err != nil {
				return err
			}
			defer file.Close()
			reader = file
		}
		var err error
		result, err = readReport(reader)
		if err != nil {
			return err
		}
	} else {
		_, inherited := os.LookupEnv("DAGGER_SESSION_PORT")
		clientOpts, err := connectionOptions(opts, inherited)
		if err != nil {
			return err
		}
		client, err := dagger.Connect(ctx, append(clientOpts, dagger.WithLogOutput(stderr))...)
		if err != nil {
			return err
		}
		defer client.Close()
		result, err = collect(ctx, client, opts)
		if err != nil {
			return err
		}
	}
	if err := result.match(opts.files, opts.paths); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func connectionOptions(opts options, inherited bool) ([]dagger.ClientOpt, error) {
	if inherited && (opts.workspace != "" || opts.workdir != "") {
		return nil, fmt.Errorf("an inherited engine session cannot honor -workspace or -workdir; set them when starting that session")
	}
	var out []dagger.ClientOpt
	if opts.workspace != "" {
		out = append(out, dagger.WithWorkspace(opts.workspace))
	}
	if opts.workdir != "" {
		out = append(out, dagger.WithWorkdir(opts.workdir))
	}
	return out, nil
}
