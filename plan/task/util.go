package task

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"go.dagger.io/dagger/compiler"
)

func withCustomName(v *compiler.Value, format string, a ...interface{}) llb.ConstraintsOpt {
	pg := progressGroup(v)
	return combineConstraints(
		llb.ProgressGroup(pg.Id, pg.Name, pg.Weak),
		llb.WithCustomName(fmt.Sprintf(format, a...)),
	)
}

func progressGroup(v *compiler.Value) *pb.ProgressGroup {
	return &pb.ProgressGroup{Id: v.Path().String()}
}

// FIXME: ResolveImageConfig is missing support for setting ProgressGroup, so we have to
// fallback to putting both the component and vertex name into the name. This needs to
// be fixed upstream in Buildkit.
func resolveImageConfigLogName(v *compiler.Value, format string, a ...interface{}) string {
	pg := progressGroup(v)
	prefix := fmt.Sprintf("@%s@", pg.Id)
	name := fmt.Sprintf(format, a...)
	return prefix + " " + name
}

func ParseResolveImageConfigLog(name string) (string, string) {
	// Pattern: `@name@ message`. Minimal length is len("@X@ ")
	if len(name) < 2 || !strings.HasPrefix(name, "@") {
		return "", name
	}

	prefixEndPos := strings.Index(name[1:], "@")
	if prefixEndPos == -1 {
		return "", name
	}

	component := name[1 : prefixEndPos+1]
	return component, name[prefixEndPos+3:]
}

func clientFilePath(path string) (string, error) {
	expanded, err := homedir.Expand(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

func combineConstraints(cs ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return constraintsOptFunc(func(m *llb.Constraints) {
		for _, c := range cs {
			c.SetConstraintsOption(m)
		}
	})
}

// FIXME: this is in Buildkit too but isn't public, should be made so
type constraintsOptFunc func(m *llb.Constraints)

func (fn constraintsOptFunc) SetConstraintsOption(m *llb.Constraints) {
	fn(m)
}

func (fn constraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	fn(&ei.Constraints)
}

func (fn constraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	fn(&li.Constraints)
}

func (fn constraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	fn(&hi.Constraints)
}

func (fn constraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	fn(&ii.Constraints)
}

func (fn constraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	fn(&gi.Constraints)
}
