package templates

import (
	"embed"
	"fmt"
	"io/fs"
)

var (
	//go:embed *.tpl
	tpl embed.FS
)

func Get(lang string) ([]byte, error) {
	return fs.ReadFile(tpl, fmt.Sprintf("%s.tpl", lang))
}
