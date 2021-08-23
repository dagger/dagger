package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
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

type Value struct {
	Name        string
	Type        string
	Description string
}

type Field struct {
	Name        string
	Description string
	Inputs      []Value
	Outputs     []Value
}

type Package struct {
	Name        string
	ShortName   string
	Description string
	Fields      []Field
}

func Parse(ctx context.Context, packageName string, val *compiler.Value) *Package {
	lg := log.Ctx(ctx)

	parseValues := func(field string, values []*compiler.Value) []Value {
		val := []Value{}

		for _, i := range values {
			v := Value{}
			v.Name = strings.TrimPrefix(
				i.Path().String(),
				field+".",
			)
			v.Type = common.FormatValue(i)
			v.Description = common.ValueDocOneLine(i)
			val = append(val, v)
		}

		return val
	}

	fields, err := val.Fields(cue.Definitions(true))
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get fields")
	}

	pkg := &Package{}
	// Package Name + Description
	pkg.Name = packageName
	parts := strings.Split(packageName, "/")
	pkg.ShortName = parts[len(parts)-1:][0]
	pkg.Description = common.ValueDocFull(val)

	// Package Fields
	for _, f := range fields {
		field := Field{}

		if !f.Selector.IsDefinition() {
			// not a definition, skipping
			continue
		}

		name := f.Label()
		v := f.Value
		if v.Cue().IncompleteKind() != cue.StructKind {
			// not a struct, skipping
			continue
		}

		// Field Name + Description
		field.Name = name
		field.Description = common.ValueDocOneLine(v)

		// Inputs
		inp := environment.ScanInputs(ctx, v)
		field.Inputs = parseValues(field.Name, inp)

		// Outputs
		out := environment.ScanOutputs(ctx, v)
		field.Outputs = parseValues(field.Name, out)

		pkg.Fields = append(pkg.Fields, field)
	}

	return pkg
}

func (p *Package) Format(f string) string {
	switch f {
	case textFormat:
		return p.Text()
	case jsonFormat:
		return p.JSON()
	case markdownFormat:
		return p.Markdown()
	default:
		panic(f)
	}
}

func (p *Package) JSON() string {
	data, err := json.MarshalIndent(p, "", "    ")
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s\n", data)
}

func (p *Package) Text() string {
	w := &strings.Builder{}
	fmt.Fprintf(w, "Package %s\n", p.Name)
	fmt.Fprintf(w, "\n%s\n", p.Description)

	fmt.Fprintf(w, "\nimport %q\n", p.Name)

	printValuesText := func(values []Value) {
		tw := tabwriter.NewWriter(w, 0, 4, len(textPadding), ' ', 0)
		for _, i := range values {
			fmt.Fprintf(tw, "\t\t%s\t%s\t%s\n",
				i.Name, i.Type, terminalTrim(i.Description))
		}
		tw.Flush()
	}

	// Package Fields
	for _, field := range p.Fields {
		fmt.Fprintf(w, "\n%s.%s\n\n%s%s\n", p.ShortName, field.Name, textPadding, field.Description)
		if len(field.Inputs) == 0 {
			fmt.Fprintf(w, "\n%sInputs: none\n", textPadding)
		} else {
			fmt.Fprintf(w, "\n%sInputs:\n", textPadding)
			printValuesText(field.Inputs)
		}

		if len(field.Outputs) == 0 {
			fmt.Fprintf(w, "\n%sOutputs: none\n", textPadding)
		} else {
			fmt.Fprintf(w, "\n%sOutputs:\n", textPadding)
			printValuesText(field.Outputs)
		}
	}

	return w.String()
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

func (p *Package) Markdown() string {
	w := &strings.Builder{}

	fmt.Fprintf(w, "---\nsidebar_label: %s\n---\n\n",
		filepath.Base(p.Name),
	)

	fmt.Fprintf(w, "# %s\n", mdEscape(p.Name))
	if p.Description != "-" {
		fmt.Fprintf(w, "\n%s\n", mdEscape(p.Description))
	}

	fmt.Fprintf(w, "\n```cue\nimport %q\n```\n", p.Name)

	printValuesMarkdown := func(values []Value) {
		tw := tabwriter.NewWriter(w, 0, 4, len(textPadding), ' ', 0)
		fmt.Fprintf(tw, "| Name\t| Type\t| Description    \t|\n")
		fmt.Fprintf(tw, "| -------------\t|:-------------:\t|:-------------:\t|\n")
		for _, i := range values {
			fmt.Fprintf(tw, "|*%s*\t| `%s`\t|%s\t|\n",
				i.Name,
				mdEscape(i.Type),
				mdEscape(i.Description),
			)
		}
		tw.Flush()
	}

	// Package Fields
	for _, field := range p.Fields {
		fieldLabel := fmt.Sprintf("%s.%s", p.ShortName, field.Name)

		fmt.Fprintf(w, "\n## %s\n\n", fieldLabel)
		if field.Description != "-" {
			fmt.Fprintf(w, "%s\n\n", mdEscape(field.Description))
		}

		fmt.Fprintf(w, "### %s Inputs\n\n", mdEscape(fieldLabel))
		if len(field.Inputs) == 0 {
			fmt.Fprintf(w, "_No input._\n")
		} else {
			printValuesMarkdown(field.Inputs)
		}

		fmt.Fprintf(w, "\n### %s Outputs\n\n", mdEscape(fieldLabel))
		if len(field.Outputs) == 0 {
			fmt.Fprintf(w, "_No output._\n")
		} else {
			printValuesMarkdown(field.Outputs)
		}
	}

	return w.String()
}

func mdEscape(s string) string {
	escape := []string{"|", "<", ">"}
	for _, c := range escape {
		s = strings.ReplaceAll(s, c, `\`+c)
	}
	return s
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

		doneCh := common.TrackCommand(ctx, cmd)

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
		p := Parse(ctx, packageName, val)
		fmt.Printf("%s", p.Format(format))

		<-doneCh
	},
}

func init() {
	docCmd.Flags().StringP("format", "f", textFormat, "Output format (txt|md)")
	docCmd.Flags().StringP("output", "o", "", "Output directory")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
}

func loadCode(packageName string) (*compiler.Value, error) {
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
	}

	src, err := compiler.Build("/config", sources, packageName)
	if err != nil {
		return nil, err
	}

	return src, nil
}

// walkStdlib generate whole docs from stdlib walk
func walkStdlib(ctx context.Context, output, format string) {
	lg := log.Ctx(ctx)

	lg.Info().Str("output", output).Msg("generating stdlib")

	packages := map[string]*Package{}
	err := fs.WalkDir(stdlib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || !d.IsDir() || d.Name() == "cue.mod" {
			return nil
		}

		// Ignore tests directories
		if d.Name() == "tests" {
			return nil
		}

		pkgName := fmt.Sprintf("alpha.dagger.io/%s", p)
		lg.Info().Str("package", pkgName).Str("format", format).Msg("generating doc")
		val, err := loadCode(pkgName)
		if err != nil {
			if strings.Contains(err.Error(), "no CUE files") {
				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
				return nil
			}
			return err
		}

		pkg := Parse(ctx, pkgName, val)
		packages[p] = pkg
		return nil
	})

	if err != nil {
		lg.Fatal().Err(err).Msg("cannot generate stdlib doc")
	}

	hasSubPackages := func(name string) bool {
		for p := range packages {
			if strings.HasPrefix(p, name+"/") {
				return true
			}
		}
		return false
	}

	// get filename from a package name
	getFileName := func(p string) string {
		filename := fmt.Sprintf("%s.%s", p, format)
		// If this package has sub-packages (e.g. `aws`), create
		// `aws/README.md` instead of `aws.md`.
		if hasSubPackages(p) {
			filename = fmt.Sprintf("%s/README.%s", p, format)
		}
		return filename
	}

	// Create main index
	index, err := os.Create(path.Join(output, "README.md"))
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot generate stdlib doc index")
	}
	defer index.Close()
	fmt.Fprintf(index, "# Index\n\n")
	indexKeys := []string{}

	for p, pkg := range packages {
		filename := getFileName(p)
		filepath := path.Join(output, filename)

		if err := os.MkdirAll(path.Dir(filepath), 0755); err != nil {
			lg.Fatal().Err(err).Msg("cannot create directory")
		}

		f, err := os.Create(filepath)
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot create file")
		}
		defer f.Close()

		indexKeys = append(indexKeys, p)
		fmt.Fprintf(f, "%s", pkg.Format(format))
	}

	// Generate index from sorted list of packages
	sort.Strings(indexKeys)
	for _, p := range indexKeys {
		description := mdEscape(packages[p].Description)
		fmt.Fprintf(index, "- [%s](./%s) - %s\n", p, getFileName(p), description)
	}
}
