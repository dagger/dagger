package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"cuelang.org/go/cue"
	"github.com/charmbracelet/glamour"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plancontext"
	"golang.org/x/term"
)

const (
	textFormat     = "txt"
	markdownFormat = "md"
	jsonFormat     = "json"
	textPadding    = "    "
)

type Field struct {
	Name        string
	Type        string
	Description string
}

type Action struct {
	Name        string
	Description string
	Fields      []Field
}

type Package struct {
	Name        string
	ShortName   string
	Description string
	Actions     []Action
}

func Parse(ctx context.Context, packageName string, val *compiler.Value) *Package {
	lg := log.Ctx(ctx)

	// parseValues := func(field string, values []*compiler.Value) []Field {
	// 	val := []Field{}

	// 	for _, i := range values {
	// 		v := Field{}
	// 		v.Name = strings.TrimPrefix(
	// 			i.Path().String(),
	// 			field+".",
	// 		)
	// 		v.Type = common.FormatValue(i)
	// 		v.Description = common.ValueDocOneLine(i)
	// 		val = append(val, v)
	// 	}

	// 	return val
	// }

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
		if !f.Selector.IsDefinition() {
			// not a definition, skipping
			continue
		}

		if f.Value.IncompleteKind() != cue.StructKind {
			// not a struct, skipping
			continue
		}

		action := Action{}

		name := f.Label()
		v := f.Value

		// Field Name + Description
		action.Name = name
		action.Description = common.ValueDocOneLine(v)

		// Inputs
		action.Fields = parseFields(action.Name, v)
		// inp := scanInputs(ctx, v)
		// action.Fields = parseValues(action.Name, inp)

		// // Outputs
		// out := environment.ScanOutputs(ctx, v)
		// field.Outputs = parseValues(field.Name, out)

		pkg.Actions = append(pkg.Actions, action)
	}

	return pkg
}

func (p *Package) Format(f string) string {
	switch f {
	case textFormat:
		// out, err := glamour.Render(p.Markdown(), "dark")
		// if err != nil {
		// 	panic(err)
		// }
		// return out
		return p.Text()
	case jsonFormat:
		return p.JSON()
	case markdownFormat:
		out, err := glamour.Render(p.Markdown(), "dark")
		if err != nil {
			panic(err)
		}
		return out
		// return p.Markdown()
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

	printValuesText := func(values []Field) {
		tw := tabwriter.NewWriter(w, 0, 4, len(textPadding), ' ', 0)
		for _, i := range values {
			fmt.Fprintf(tw, "\t\t%s\t%s\t%s\n",
				i.Name, i.Type, terminalTrim(i.Description))
		}
		tw.Flush()
	}

	// Package Fields
	for _, field := range p.Actions {
		fmt.Fprintf(w, "\n%s.%s\n\n%s%s\n", p.ShortName, field.Name, textPadding, field.Description)
		if len(field.Fields) == 0 {
			fmt.Fprintf(w, "\n%sFields: none\n", textPadding)
		} else {
			fmt.Fprintf(w, "\n%sFields:\n", textPadding)
			printValuesText(field.Fields)
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

	printValuesMarkdown := func(values []Field) {
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
	for _, field := range p.Actions {
		fieldLabel := fmt.Sprintf("%s.%s", p.ShortName, field.Name)

		fmt.Fprintf(w, "\n## %s\n\n", fieldLabel)
		if field.Description != "-" {
			fmt.Fprintf(w, "%s\n\n", mdEscape(field.Description))
		}

		fmt.Fprintf(w, "### %s Fields\n\n", mdEscape(fieldLabel))
		if len(field.Fields) == 0 {
			fmt.Fprintf(w, "_No fields._\n")
		} else {
			printValuesMarkdown(field.Fields)
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

		// output := viper.GetString("output")
		// if output != "" {
		// 	if len(args) > 0 {
		// 		lg.Warn().Str("packageName", args[0]).Msg("arg is ignored when --output is set")
		// 	}
		// 	walkStdlib(ctx, output, format)
		// 	return
		// }

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
	// docCmd.Flags().StringP("output", "o", "", "Output directory")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
}

func loadCode(packageName string) (*compiler.Value, error) {
	sources := map[string]fs.FS{
		path.Join("cue.mod", "pkg"): pkg.FS,
	}

	src, err := compiler.Build("/config", sources, packageName)
	if err != nil {
		return nil, err
	}

	return src, nil
}

// // walkStdlib generate whole docs from stdlib walk
// func walkStdlib(ctx context.Context, output, format string) {
// 	lg := log.Ctx(ctx)

// 	lg.Info().Str("output", output).Msg("generating stdlib")

// 	packages := map[string]*Package{}
// 	err := fs.WalkDir(pkg.FS, pkg.AlphaModule, func(p string, d fs.DirEntry, err error) error {
// 		if err != nil {
// 			return err
// 		}

// 		// Ignore useless embedded files
// 		if p == "." || d.Name() == pkg.AlphaModule || !d.IsDir() || d.Name() == "cue.mod" ||
// 			strings.Contains(p, "cue.mod") || strings.Contains(p, "tests") {
// 			return nil
// 		}

// 		p = strings.TrimPrefix(p, pkg.AlphaModule+"/")

// 		// Ignore tests directories
// 		if d.Name() == "tests" {
// 			return nil
// 		}

// 		pkgName := fmt.Sprintf("%s/%s", pkg.AlphaModule, p)
// 		lg.Info().Str("package", pkgName).Str("format", format).Msg("generating doc")
// 		val, err := loadCode(pkgName)
// 		if err != nil {
// 			if strings.Contains(err.Error(), "no CUE files") {
// 				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
// 				return nil
// 			}
// 			if strings.Contains(err.Error(), "cannot find package") {
// 				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
// 				return nil
// 			}
// 			return err
// 		}

// 		pkg := Parse(ctx, pkgName, val)
// 		packages[p] = pkg
// 		return nil
// 	})

// 	if err != nil {
// 		lg.Fatal().Err(err).Msg("cannot generate stdlib doc")
// 	}

// 	hasSubPackages := func(name string) bool {
// 		for p := range packages {
// 			if strings.HasPrefix(p, name+"/") {
// 				return true
// 			}
// 		}
// 		return false
// 	}

// 	// get filename from a package name
// 	getFileName := func(p string) string {
// 		filename := fmt.Sprintf("%s.%s", p, format)
// 		// If this package has sub-packages (e.g. `aws`), create
// 		// `aws/README.md` instead of `aws.md`.
// 		if hasSubPackages(p) {
// 			filename = fmt.Sprintf("%s/README.%s", p, format)
// 		}
// 		return filename
// 	}

// 	// Create main index
// 	index, err := os.Create(path.Join(output, "README.md"))
// 	if err != nil {
// 		lg.Fatal().Err(err).Msg("cannot generate stdlib doc index")
// 	}
// 	defer index.Close()
// 	fmt.Fprintf(index, "# Index\n\n")
// 	indexKeys := []string{}

// 	for p, pkg := range packages {
// 		filename := getFileName(p)
// 		filepath := path.Join(output, filename)

// 		if err := os.MkdirAll(path.Dir(filepath), 0755); err != nil {
// 			lg.Fatal().Err(err).Msg("cannot create directory")
// 		}

// 		f, err := os.Create(filepath)
// 		if err != nil {
// 			lg.Fatal().Err(err).Msg("cannot create file")
// 		}
// 		defer f.Close()

// 		indexKeys = append(indexKeys, p)
// 		fmt.Fprintf(f, "%s", pkg.Format(format))
// 	}

// 	// Generate index from sorted list of packages
// 	sort.Strings(indexKeys)
// 	for _, p := range indexKeys {
// 		description := mdEscape(packages[p].Description)
// 		fmt.Fprintf(index, "- [%s](./%s) - %s\n", p, getFileName(p), description)
// 	}
// }

func isReference(val cue.Value) bool {
	isRef := func(v cue.Value) bool {
		_, ref := v.ReferencePath()

		if ref.String() == "" || v.Path().String() == ref.String() {
			// not a reference
			return false
		}

		for _, s := range ref.Selectors() {
			if s.IsDefinition() {
				// if we reference to a definition, we skip the check
				return false
			}
		}

		return true
	}

	op, vals := val.Expr()
	if op == cue.NoOp {
		return isRef(val)
	}

	for _, v := range vals {
		// if the expr has an op (& or |, etc...), check the expr values, recursively
		if isReference(v) {
			return true
		}
	}

	return isRef(val)
}

// func parseFields(action string, v *compiler.Value) []Field {
// 	val := []Field{}

// 	fields, err := v.Fields()
// 	if err != nil {
// 		panic(err)
// 		return nil
// 	}

// 	for _, f := range fields {
// 		v := Field{}
// 		v.Name = strings.TrimPrefix(
// 			f.Value.Path().String(),
// 			action+".",
// 		)
// 		v.Type = common.FormatValue(f.Value)
// 		v.Description = common.ValueDocOneLine(f.Value)
// 		val = append(val, v)
// 	}

// 	return val
// }

func parseFields(action string, v *compiler.Value) []Field {
	fields := []Field{}

	v.Walk(
		func(val *compiler.Value) bool {
			if val.Path().String() == v.Path().String() {
				return true
			}
			// if val.IncompleteKind() != cue.StructKind && val.IsConcrete() {
			// 	return false
			// }
			field := Field{}
			field.Name = strings.TrimPrefix(
				val.Path().String(),
				action+".",
			)
			field.Type = common.FormatValue(val)
			field.Description = common.ValueDocOneLine(val)

			fields = append(fields, field)

			return false
		}, nil,
	)

	return fields
}

func scanInputs(ctx context.Context, value *compiler.Value) []*compiler.Value {
	inputs := []*compiler.Value{}

	value.Walk(
		func(val *compiler.Value) bool {
			switch {
			case plancontext.IsFSValue(val):
				inputs = append(inputs, val)
				return false
			case plancontext.IsSecretValue(val):
				inputs = append(inputs, val)
				return false
			case plancontext.IsServiceValue(val):
				inputs = append(inputs, val)
				return false
			}
			// if isReference(val.Cue()) {
			// 	return true
			// }

			// if val.IsConcrete() {
			// 	return true
			// }

			// if !val.HasAttr("input") {
			// 	return true
			// }

			inputs = append(inputs, val)

			return true
		}, nil,
	)

	return inputs
}
