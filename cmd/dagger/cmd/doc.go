package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"cuelang.org/go/cue"
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
	jsonFormat     = "json"
	textPadding    = "    "
)

// types used for json generation

type ValueJSON struct {
	Name        string
	Type        string
	Description string
}

type FieldJSON struct {
	Name        string
	Description string
	Inputs      []ValueJSON
	Outputs     []ValueJSON
}

type PackageJSON struct {
	Name        string
	Description string
	Fields      []FieldJSON
}

var docCmd = &cobra.Command{
	Use:   "doc [PACKAGE | PATH]",
	Short: "document a package",
	Args:  cobra.MaximumNArgs(1),
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

		format := viper.GetString("format")
		if format != textFormat &&
			format != markdownFormat &&
			format != jsonFormat {
			lg.Fatal().Msg("format must be either `txt`, `md` or `json`")
		}

		output := viper.GetString("output")
		if output != "" {
			if len(args) > 0 {
				lg.Warn().Str("packageName", args[0]).Msg("arg is ignored when --output is set")
			}
			walkStdlib(ctx, output, format)
			return
		}

		if len(args) < 1 {
			lg.Fatal().Msg("need to specify package name in command argument")
		}

		packageName := args[0]

		val, err := loadCode(packageName)
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot compile code")
		}
		PrintDoc(ctx, os.Stdout, packageName, val, format)
	},
}

func init() {
	docCmd.Flags().StringP("format", "f", textFormat, "Output format (txt|md)")
	docCmd.Flags().StringP("output", "o", "", "Output directory")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
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

// printValuesText (text) formats an array of Values on stdout
func printValuesText(iw io.Writer, libName string, values []*compiler.Value) {
	w := tabwriter.NewWriter(iw, 0, 4, len(textPadding), ' ', 0)
	for _, i := range values {
		docStr := terminalTrim(common.ValueDocOneLine(i))
		fmt.Fprintf(w, "\t\t%s\t%s\t%s\n",
			formatLabel(libName, i), common.FormatValue(i), docStr)
	}
	w.Flush()
}

// printValuesMarkdown (markdown) formats an array of Values on stdout
func printValuesMarkdown(iw io.Writer, libName string, values []*compiler.Value) {
	w := tabwriter.NewWriter(iw, 0, 4, len(textPadding), ' ', 0)
	fmt.Fprintf(w, "| Name\t| Type\t| Description    \t|\n")
	fmt.Fprintf(w, "| -------------\t|:-------------:\t|:-------------:\t|\n")
	for _, i := range values {
		fmt.Fprintf(w, "|*%s*\t| `%s`\t|%s\t|\n",
			formatLabel(libName, i),
			mdEscape(common.FormatValue(i)),
			mdEscape(common.ValueDocOneLine(i)))
	}
	w.Flush()
}

// printValuesJson fills a struct for json output
func valuesToJSON(libName string, values []*compiler.Value) []ValueJSON {
	val := []ValueJSON{}

	for _, i := range values {
		v := ValueJSON{}
		v.Name = formatLabel(libName, i)
		v.Type = common.FormatValue(i)
		v.Description = common.ValueDocOneLine(i)
		val = append(val, v)
	}

	return val
}

func PrintDoc(ctx context.Context, w io.Writer, packageName string, val *compiler.Value, format string) {
	lg := log.Ctx(ctx)

	fields, err := val.Fields(cue.Definitions(true))
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get fields")
	}

	packageJSON := &PackageJSON{}
	// Package Name + Description
	switch format {
	case textFormat:
		fmt.Fprintf(w, "Package %s\n", packageName)
		fmt.Fprintf(w, "\n%s\n", common.ValueDocFull(val))
	case markdownFormat:
		fmt.Fprintf(w, "---\nsidebar_label: %s\n---\n\n",
			filepath.Base(packageName),
		)
		fmt.Fprintf(w, "# %s\n", mdEscape(packageName))
		comment := common.ValueDocFull(val)
		if comment == "-" {
			break
		}
		fmt.Fprintf(w, "\n%s\n", mdEscape(comment))
	case jsonFormat:
		packageJSON.Name = packageName
		comment := common.ValueDocFull(val)
		if comment != "-" {
			packageJSON.Description = comment
		}
	}

	// Package Fields
	for _, field := range fields {
		fieldJSON := FieldJSON{}

		if !field.Selector.IsDefinition() {
			// not a definition, skipping
			continue
		}

		name := field.Label()
		v := field.Value
		if v.Cue().IncompleteKind() != cue.StructKind {
			// not a struct, skipping
			continue
		}

		// Field Name + Description
		comment := common.ValueDocOneLine(v)
		switch format {
		case textFormat:
			fmt.Fprintf(w, "\n%s\n\n%s%s\n", name, textPadding, comment)
		case markdownFormat:
			fmt.Fprintf(w, "\n## %s\n\n", name)
			if comment != "-" {
				fmt.Fprintf(w, "%s\n\n", mdEscape(comment))
			}
		case jsonFormat:
			fieldJSON.Name = name
			comment := common.ValueDocOneLine(val)
			if comment != "-" {
				fieldJSON.Description = comment
			}
		}

		// Inputs
		inp := environment.ScanInputs(ctx, v)
		switch format {
		case textFormat:
			if len(inp) == 0 {
				fmt.Fprintf(w, "\n%sInputs: none\n", textPadding)
				break
			}
			fmt.Fprintf(w, "\n%sInputs:\n", textPadding)
			printValuesText(w, name, inp)
		case markdownFormat:
			fmt.Fprintf(w, "### %s Inputs\n\n", mdEscape(name))
			if len(inp) == 0 {
				fmt.Fprintf(w, "_No input._\n")
				break
			}
			printValuesMarkdown(w, name, inp)
		case jsonFormat:
			fieldJSON.Inputs = valuesToJSON(name, inp)
		}

		// Outputs
		out := environment.ScanOutputs(ctx, v)
		switch format {
		case textFormat:
			if len(out) == 0 {
				fmt.Fprintf(w, "\n%sOutputs: none\n", textPadding)
				break
			}
			fmt.Fprintf(w, "\n%sOutputs:\n", textPadding)
			printValuesText(w, name, out)
		case markdownFormat:
			fmt.Fprintf(w, "\n### %s Outputs\n\n", mdEscape(name))
			if len(out) == 0 {
				fmt.Fprintf(w, "_No output._\n")
				break
			}
			printValuesMarkdown(w, name, out)
		case jsonFormat:
			fieldJSON.Outputs = valuesToJSON(name, out)
			packageJSON.Fields = append(packageJSON.Fields, fieldJSON)
		}
	}

	if format == jsonFormat {
		data, err := json.MarshalIndent(packageJSON, "", "    ")
		if err != nil {
			lg.Fatal().Err(err).Msg("json marshal")
		}
		fmt.Fprintf(w, "%s\n", data)
	}
}

// walkStdlib generate whole docs from stdlib walk
func walkStdlib(ctx context.Context, output, format string) {
	lg := log.Ctx(ctx)

	lg.Info().Str("output", output).Msg("generating stdlib")
	err := fs.WalkDir(stdlib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || !d.IsDir() {
			return nil
		}

		filename := fmt.Sprintf("%s.%s", p, format)
		filepath := path.Join(output, filename)

		if err := os.MkdirAll(path.Dir(filepath), 0755); err != nil {
			return err
		}

		f, err := os.Create(filepath)
		if err != nil {
			return err
		}
		defer f.Close()

		pkg := fmt.Sprintf("dagger.io/%s", p)
		lg.Info().Str("package", pkg).Str("format", format).Msg("generating doc")
		val, err := loadCode(pkg)
		if err != nil {
			if strings.Contains(err.Error(), "no CUE files") {
				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
				return nil
			}
			return err
		}

		PrintDoc(ctx, f, pkg, val, format)
		return nil
	})

	if err != nil {
		lg.Fatal().Err(err).Msg("cannot generate stdlib doc")
	}
}
