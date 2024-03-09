package main

import (
	"io"
	"os"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/vito/progrock/ui"
)

func main() {
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	var idp call.ID
	if err := idp.Decode(strings.TrimSpace(string(bytes))); err != nil {
		panic(err)
	}
	fe := idtui.New()
	if err := fe.DumpID(ui.NewOutput(os.Stdout), &idp); err != nil {
		panic(err)
	}
}
