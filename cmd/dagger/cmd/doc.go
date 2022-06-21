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
	"go.dagger.io/dagger/pkg"
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
	Values      []Value
}

type Package struct {
	Name        string
	ShortName   string
	Description string
	Fields      []Field
}

func parseValues(_ context.Context, field *Field, cueField *compiler.Field) error {
	fields, err := cueField.Value.Fields()
	if err != nil {
		return err
	}

	for _, f := range fields {
		if f.Value.IsConcrete() {
			// Skip values that cannot be set (concrete)
			continue
		}
		val := &Value{
			Name:        f.Label(),
			Type:        f.Value.IncompleteKind().String(),
			Description: common.ValueDocOneLine(f.Value),
		}
		field.Values = append(field.Values, *val)
	}

	return nil
}

func Parse(ctx context.Context, packageName string, val *compiler.Value) *Package {
	lg := log.Ctx(ctx)

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
	for _, v := range fields {
		f := v
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

		if err := parseValues(ctx, &field, &f); err != nil {
			lg.Warn().Str("fieldName", name).Err(err).Msg("cannot get field values, ignoring field")
		}
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
		printValuesText(field.Values)
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

		printValuesMarkdown(field.Values)
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
	// FIXME: this command is currently broken as of 0.2.
	Hidden: true,
	Use:    "doc [PACKAGE | PATH]",
	Short:  "document a package",
	Args:   cobra.MaximumNArgs(1),
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
			walkPackages(ctx, output, format)
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
		path.Join("cue.mod", "pkg"): pkg.FS,
	}

	src, err := compiler.Build(context.TODO(), "/config", sources, packageName)
	if err != nil {
		return nil, err
	}

	return src, nil
}

func walkPackage(ctx context.Context, packages map[string]*Package, packageName, format string) {
	lg := log.Ctx(ctx)

	err := fs.WalkDir(pkg.FS, packageName, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Ignore useless embedded files
		if p == "." || d.Name() == packageName || !d.IsDir() || d.Name() == "cue.mod" ||
			strings.Contains(p, "cue.mod") || strings.Contains(p, "tests") {
			return nil
		}

		p = strings.TrimPrefix(p, packageName+"/")

		// Ignore tests directories
		if d.Name() == "tests" {
			return nil
		}

		lg.Info().Str("package", packageName).Str("format", format).Msg("generating doc")
		val, err := loadCode(packageName)
		if err != nil {
			if strings.Contains(err.Error(), "no CUE files") {
				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
				return nil
			}
			if strings.Contains(err.Error(), "cannot find package") {
				lg.Warn().Str("package", p).Err(err).Msg("ignoring")
				return nil
			}
			return err
		}

		pkg := Parse(ctx, packageName, val)
		packages[p] = pkg
		return nil
	})

	if err != nil {
		lg.Fatal().Err(err).Msg("cannot generate stdlib doc")
	}
}

// walkPackages generate whole docs from stdlib walk
func walkPackages(ctx context.Context, output, format string) {
	lg := log.Ctx(ctx)

	lg.Info().Str("output", output).Msg("generating stdlib")

	packages := map[string]*Package{}
	// FIXME: the recursive walk is broken
	walkPackage(ctx, packages, "dagger.io/dagger", format)

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
	// FIXME: I removed a \n character, so that markdownlint doesn't complain
	//        about an extra newline at the end of the file.
	fmt.Fprintf(index, "# Index\n")
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
	// Add a extra blank line if we have at least one package
	// TODO: this is a hack, fixes issue with markdownlint, if we haven't generated any docs.
	if len(indexKeys) > 0 {
		fmt.Fprintf(index, "\n")
	}
	for _, p := range indexKeys {
		description := mdEscape(packages[p].Description)
		fmt.Fprintf(index, "- [%s](./%s) - %s\n", p, getFileName(p), description)
	}
}
