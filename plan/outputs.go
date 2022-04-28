package plan

import (
	"bytes"
	"fmt"
	"os"
	"text/tabwriter"

	"cuelang.org/go/cue"
	"cuelang.org/go/pkg/encoding/yaml"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/compiler"
)

func PrintOutputs(fields []compiler.Field, format, file string) error {
	s, err := decodeOutputs(fields, format)
	if err != nil {
		return err
	}

	if file == "" {
		fmt.Print(s)
		return nil
	}

	return os.WriteFile(file, []byte(s), 0600)
}

func decodeOutputs(fields []compiler.Field, format string) (string, error) {
	// do plain first because it doesn't need a compiled Value
	if format == "plain" || format == "" {
		if len(fields) == 0 {
			return "", nil
		}

		buf := new(bytes.Buffer)
		w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)

		fmt.Fprintln(w, "Field\tValue")
		for _, f := range fields {
			fmt.Fprintf(w, "%s\t%s\n", f.Label(), common.FormatValue(f.Value))
		}

		w.Flush()
		return buf.String(), nil
	}

	// compile a Value for next formats
	v := compiler.NewValue()
	for _, f := range fields {
		if err := v.FillPath(cue.MakePath(f.Selector), f.Value); err != nil {
			return "", err
		}
	}

	switch format {
	case "json":
		return v.JSON().PrettyString(), nil

	case "yaml":
		return yaml.Marshal(v.Cue())

	default:
		return "", fmt.Errorf("invalid output format %q", format)
	}
}
