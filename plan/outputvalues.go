package plan

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"cuelang.org/go/cue"
	"cuelang.org/go/pkg/encoding/yaml"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/compiler"
)

func ListOutputs(ctx context.Context, p *Plan, computed *compiler.Value, format, file string) error {
	lg := log.Ctx(ctx)
	path := cue.ParsePath("outputs.values")

	if !p.Source().LookupPath(path).Exists() {
		return nil
	}

	out := compiler.NewValue()

	if err := out.FillPath(cue.MakePath(), p.Source()); err != nil {
		return err
	}

	if err := out.FillPath(cue.MakePath(), computed); err != nil {
		return err
	}

	vals := out.LookupPath(path)

	// Avoid confusion on missing values by forcing concreteness
	if err := vals.IsConcreteR(); err != nil {
		return err
	}

	s, err := decodeOutput(vals, format)
	if err != nil {
		return err
	}

	if file == "" {
		lg.Info().Msg(fmt.Sprintf("Output:\n%v", s))
		return nil
	}

	return os.WriteFile(file, []byte(s), 0600)
}

func decodeOutput(vals *compiler.Value, format string) (string, error) {
	switch format {
	case "json":
		s := vals.JSON().PrettyString()
		return s, nil

	case "yaml":
		s, err := yaml.Marshal(vals.Cue())
		if err != nil {
			return "", err
		}
		return s, nil

	// The simplest and default case is to have outputs as
	// a struct of strings and print it as a table.
	case "plain", "":
		buf := new(bytes.Buffer)
		w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "Field\tValue")

		if fields, err := vals.Fields(); err == nil {
			for _, out := range fields {
				fmt.Fprintf(w, "%s\t%s\n", out.Label(), common.FormatValue(out.Value))
			}
		}

		w.Flush()
		return buf.String(), nil
	}

	return "", fmt.Errorf("invalid --output-format %q", format)
}
