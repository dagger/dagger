package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/stdlib"
	"golang.org/x/term"
)

const (
	textFormat     = "txt"
	markdownFormat = "md"
	textPadding    = "    "
)

var docCmd = &cobra.Command{
	Use:   "doc [PACKAGE | PATH]",
	Short: "document a package",
	Args:  cobra.ExactArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())

		format := viper.GetString("output")
		if format != textFormat && format != markdownFormat {
			lg.Fatal().Msg("output must be either `txt` or `md`")
		}

		packageName := args[0]

		val, err := loadCode(packageName)
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot compile code")
		}
		PrintDoc(ctx, packageName, val, format)
	},
}

func init() {
	docCmd.Flags().StringP("output", "o", textFormat, "Output format (txt|md)")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
}

func extractSpec(v cue.Value) string {
	node := v.Source()
	if node == nil {
		return fmt.Sprintf("%v", v)
	}
	src, err := format.Node(node)
	if err != nil {
		panic(err)
	}
	space := regexp.MustCompile(`[\s\n]+`)
	return strings.TrimSpace(
		space.ReplaceAllString(string(src), " "),
	)
}

func mdEscape(s string) string {
	escape := []string{"|", "<", ">"}
	for _, c := range escape {
		s = strings.ReplaceAll(s, c, `\`+c)
	}
	return s
}

func terminalTrim(msg string) string {
	// If we're not running on a terminal, return the whole string
	size, _, err := term.GetSize(1)
	if err != nil {
		return msg
	}

	// Otherwise, trim to fit half the terminal
	size /= 2
	for utf8.RuneCountInString(msg) > size {
		msg = msg[0:len(msg)-4] + "…"
	}
	return msg
}

func formatLabel(name string, val *compiler.Value) string {
	label := val.Path().String()
	return strings.TrimPrefix(label, name+".")
}

func loadCode(packageName string) (*compiler.Value, error) {
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
	}

	src, err := compiler.Build(sources, packageName)
	if err != nil {
		return nil, err
	}

	return src, nil
}

func PrintDoc(ctx context.Context, packageName string, val *compiler.Value, format string) {
	lg := log.Ctx(ctx)

	fields, err := val.Fields(cue.Definitions(true))
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get fields")
	}

	comment := common.ValueDocString(val)

	// Print title
	switch format {
	case textFormat:
		fmt.Printf("Package %s\n", packageName)
		if comment != "" {
			fmt.Printf("\n%s\n", comment)
		}
	case markdownFormat:
		importPath := strings.Split(packageName, "/")
		switch {
		case len(importPath) == 2:
			fmt.Printf("## %s\n", importPath[1])
		case len(importPath) > 2:
			fmt.Printf("### %s\n", strings.Join(importPath[2:], "/"))
		default:
			fmt.Printf("## %s\n", packageName)
		}
		if comment != "" {
			fmt.Printf("\n%s\n", comment)
		}
	}

	for _, field := range fields {
		if !field.Selector.IsDefinition() {
			continue
		}

		name := field.Label()
		v := field.Value
		if v.Cue().IncompleteKind() != cue.StructKind {
			continue
		}

		// Package name + comment
		comment := common.ValueDocString(v)
		switch format {
		case textFormat:
			fmt.Printf("\n%s\n\n%s%s\n", name, textPadding, comment)
		case markdownFormat:
			if comment != "" {
				comment = fmt.Sprintf("\n\n%s", comment)
			}
			fmt.Printf("\n#### %s%s\n\n", name, mdEscape(comment))
			fmt.Printf("##### Fields\n\n")
		}

		// Package inputs
		inp := environment.ScanInputs(ctx, v)
		switch format {
		case textFormat:
			if len(inp) == 0 {
				fmt.Printf("\n%sInputs: none\n", textPadding)
				break
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, len(textPadding), ' ', 0)
			fmt.Printf("\n%sInputs:\n", textPadding)
			for _, i := range inp {
				docStr := terminalTrim(common.ValueDocString(i))
				fmt.Fprintf(w, "\t\t%s\t%s\t%s\n",
					formatLabel(name, i), common.FormatValue(i), docStr)
			}
			w.Flush()
		case markdownFormat:
			// todo
		}

		// Package outputs
		out := environment.ScanOutputs(ctx, v)
		switch format {
		case textFormat:
			if len(out) == 0 {
				fmt.Printf("\n%sOutputs: none\n", textPadding)
				break
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, len(textPadding), ' ', 0)
			fmt.Printf("\n%sOutputs:\n", textPadding)
			for _, o := range out {
				docStr := terminalTrim(common.ValueDocString(o))
				fmt.Fprintf(w, "\t\t%s\t%s\t%s\n",
					formatLabel(name, o), common.FormatValue(o), docStr)
			}
			w.Flush()
		case markdownFormat:
			// todo
		}
	}

}
