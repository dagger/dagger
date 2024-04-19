package debug

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var DumpLLBCommand = cli.Command{
	Name:      "dump-llb",
	Usage:     "dump LLB in human-readable format. LLB can be also passed via stdin. This command does not require the daemon to be running.",
	ArgsUsage: "<llbfile>",
	Action:    dumpLLB,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "dot",
			Usage: "Output dot format",
		},
	},
}

func dumpLLB(clicontext *cli.Context) error {
	var r io.Reader
	if llbFile := clicontext.Args().First(); llbFile != "" && llbFile != "-" {
		f, err := os.Open(llbFile)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	} else {
		r = os.Stdin
	}
	ops, err := loadLLB(r)
	if err != nil {
		return err
	}
	if clicontext.Bool("dot") {
		writeDot(ops, os.Stdout)
	} else {
		enc := json.NewEncoder(os.Stdout)
		for _, op := range ops {
			if err := enc.Encode(op); err != nil {
				return err
			}
		}
	}
	return nil
}

type llbOp struct {
	Op         pb.Op
	Digest     digest.Digest
	OpMetadata pb.OpMetadata
}

func loadLLB(r io.Reader) ([]llbOp, error) {
	def, err := llb.ReadFrom(r)
	if err != nil {
		return nil, err
	}
	var ops []llbOp
	for _, dt := range def.Def {
		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return nil, errors.Wrap(err, "failed to parse op")
		}
		dgst := digest.FromBytes(dt)
		ent := llbOp{Op: op, Digest: dgst, OpMetadata: def.Metadata[dgst]}
		ops = append(ops, ent)
	}
	return ops, nil
}

func writeDot(ops []llbOp, w io.Writer) {
	// TODO: print OpMetadata
	fmt.Fprintln(w, "digraph {")
	defer fmt.Fprintln(w, "}")
	for _, op := range ops {
		name, shape := attr(op.Digest, op.Op)
		fmt.Fprintf(w, "  %q [label=%q shape=%q];\n", op.Digest, name, shape)
	}
	for _, op := range ops {
		for i, inp := range op.Op.Inputs {
			label := ""
			if eo, ok := op.Op.Op.(*pb.Op_Exec); ok {
				for _, m := range eo.Exec.Mounts {
					if int(m.Input) == i && m.Dest != "/" {
						label = m.Dest
					}
				}
			}
			fmt.Fprintf(w, "  %q -> %q [label=%q];\n", inp.Digest, op.Digest, label)
		}
	}
}

func attr(dgst digest.Digest, op pb.Op) (string, string) {
	switch op := op.Op.(type) {
	case *pb.Op_Source:
		return op.Source.Identifier, "ellipse"
	case *pb.Op_Exec:
		return strings.Join(op.Exec.Meta.Args, " "), "box"
	case *pb.Op_Build:
		return "build", "box3d"
	case *pb.Op_Merge:
		return "merge", "invtriangle"
	case *pb.Op_Diff:
		return "diff", "doublecircle"
	case *pb.Op_File:
		names := []string{}

		for _, action := range op.File.Actions {
			var name string

			switch act := action.Action.(type) {
			case *pb.FileAction_Copy:
				name = fmt.Sprintf("copy{src=%s, dest=%s}", act.Copy.Src, act.Copy.Dest)
			case *pb.FileAction_Mkfile:
				name = fmt.Sprintf("mkfile{path=%s}", act.Mkfile.Path)
			case *pb.FileAction_Mkdir:
				name = fmt.Sprintf("mkdir{path=%s}", act.Mkdir.Path)
			case *pb.FileAction_Rm:
				name = fmt.Sprintf("rm{path=%s}", act.Rm.Path)
			}

			names = append(names, name)
		}
		return strings.Join(names, ","), "note"
	default:
		return dgst.String(), "plaintext"
	}
}
