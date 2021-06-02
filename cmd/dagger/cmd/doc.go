package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
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
	"golang.org/x/crypto/ssh/terminal"
)

const (
	textFormat     = "txt"
	markdownFormat = "md"
)

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
		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)

		format := viper.GetString("output")
		if format != textFormat && format != markdownFormat {
			lg.Fatal().Msg("output must be either `txt` or `md`")
		}

		val, err := loadCode(args[0])
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot compile code")
		}
		PrintDoc(ctx, val, format)
	},
}

func init() {
	docCmd.Flags().StringP("output", "o", textFormat, "Output format (txt|md)")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
}

func extractComment(v cue.Value) string {
	docs := []string{}
	for _, c := range v.Doc() {
		docs = append(docs, strings.TrimSpace(c.Text()))
	}
	doc := strings.Join(docs, " ")

	lines := strings.Split(doc, "\n")

	// Strip out FIXME, TODO, and INTERNAL comments
	docs = []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "FIXME: ") ||
			strings.HasPrefix(line, "TODO: ") ||
			strings.HasPrefix(line, "INTERNAL: ") {
			continue
		}
		docs = append(docs, line)
	}
	return strings.Join(docs, " ")
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
	size, _, err := terminal.GetSize(1)
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

func loadCode(path string) (*compiler.Value, error) {
	src, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	val, err := compiler.Compile("", string(src))
	if err != nil {
		return nil, err
	}

	return val, nil
}

func PrintDoc(ctx context.Context, val *compiler.Value, format string) {
	lg := log.Ctx(ctx)

	environment.ScanOutputs(ctx, val)
}
