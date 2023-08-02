package core

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func defToDAG(def *pb.Definition) (*opDAG, error) {
	digestToOp := map[digest.Digest]*pb.Op{}
	digestToMetadata := map[digest.Digest]*pb.OpMetadata{}
	for _, dt := range def.Def {
		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return nil, errors.Wrap(err, "failed to parse llb proto op")
		}
		dgst := digest.FromBytes(dt)
		digestToOp[dgst] = &op
		metadata := def.Metadata[dgst]
		digestToMetadata[dgst] = &metadata
	}
	lastOpDigest := digest.FromBytes(def.Def[len(def.Def)-1])
	return opToDAG(
		digestToOp[lastOpDigest],
		lastOpDigest,
		0,
		digestToOp,
		digestToMetadata,
		map[digest.Digest]*opDAG{},
	)
}

func opToDAG(
	op *pb.Op,
	opDigest digest.Digest,
	outputIndex pb.OutputIndex,
	digestToOp map[digest.Digest]*pb.Op,
	digestToMetadata map[digest.Digest]*pb.OpMetadata,
	memo map[digest.Digest]*opDAG,
) (*opDAG, error) {
	if opDigest == "" {
		return nil, fmt.Errorf("unexpected empty op digest")
	}
	if dag, ok := memo[opDigest]; ok {
		outputSpecificDAG, ok := dag.allOutputs[outputIndex]
		if !ok {
			outputSpecificDAG = &opDAG{
				Op:          dag.Op,
				opDigest:    dag.opDigest,
				metadata:    dag.metadata,
				inputs:      dag.inputs,
				outputIndex: outputIndex,
				allOutputs:  dag.allOutputs,
			}
			dag.allOutputs[outputIndex] = outputSpecificDAG
		}
		return outputSpecificDAG, nil
	}
	if op == nil {
		return nil, fmt.Errorf("op with digest %q not found", opDigest)
	}
	dag := &opDAG{
		Op:          op,
		opDigest:    &opDigest,
		metadata:    digestToMetadata[opDigest],
		outputIndex: outputIndex,
		allOutputs:  map[pb.OutputIndex]*opDAG{},
	}
	dag.allOutputs[outputIndex] = dag
	memo[opDigest] = dag
	for _, input := range op.Inputs {
		inputDigest := input.Digest
		inputDAG, err := opToDAG(
			digestToOp[inputDigest],
			inputDigest,
			input.Index,
			digestToOp,
			digestToMetadata,
			memo,
		)
		if err != nil {
			return nil, err
		}
		dag.inputs = append(dag.inputs, inputDAG)
	}
	return dag, nil
}

type opDAG struct {
	*pb.Op                  // the root of the DAG
	opDigest *digest.Digest // the digest of this root, common across all outputIndexes for this root
	metadata *pb.OpMetadata // metadata for the root
	inputs   []*opDAG       // the inputs to the root

	outputIndex pb.OutputIndex            // the specific output of the op that the root represents
	allOutputs  map[pb.OutputIndex]*opDAG // all outputs of this root, including this one

	asExecOp *execOp // cached execOp conversion
}

func (dag *opDAG) String() string {
	builder := &strings.Builder{}
	return dag.toString(builder, "")
}

func (dag *opDAG) toString(builder *strings.Builder, indent string) string {
	fmt.Fprintf(builder, "%s%d %+v\n", indent, dag.outputIndex, dag.Op.Op)
	for _, input := range dag.inputs {
		input.toString(builder, indent+"  ")
	}
	return builder.String()
}

func (dag *opDAG) Walk(f func(*opDAG) error) error {
	return dag.walk(f, map[*opDAG]struct{}{})
}

func (dag *opDAG) walk(f func(*opDAG) error, memo map[*opDAG]struct{}) error {
	if _, ok := memo[dag]; ok {
		return nil
	}
	memo[dag] = struct{}{}
	if err := f(dag); err != nil {
		return err
	}
	for _, input := range dag.inputs {
		if err := input.walk(f, memo); err != nil {
			return err
		}
	}
	return nil
}

// Marshal will convert the dag back to a flat pb.Definition, updating all digests
// based on any modifications made to the dag.
func (dag *opDAG) Marshal() (*pb.Definition, error) {
	def, _, err := dag.marshal(&pb.Definition{
		Metadata: map[digest.Digest]pb.OpMetadata{},
	}, map[digest.Digest]digest.Digest{})
	return def, err
}

func (dag *opDAG) marshal(def *pb.Definition, memo map[digest.Digest]digest.Digest) (*pb.Definition, digest.Digest, error) {
	if dgst, ok := memo[*dag.opDigest]; ok {
		return def, dgst, nil
	}

	newOp := &pb.Op{
		Op:          dag.Op.Op,
		Platform:    dag.Platform,
		Constraints: dag.Constraints,
	}
	for _, input := range dag.inputs {
		updatedDef, newInputOpDigest, err := input.marshal(def, memo)
		if err != nil {
			return nil, "", err
		}
		def = updatedDef
		newOp.Inputs = append(newOp.Inputs, &pb.Input{
			Digest: newInputOpDigest,
			Index:  input.outputIndex,
		})
	}
	newOpBytes, err := newOp.Marshal()
	if err != nil {
		return nil, "", err
	}
	newOpDigest := digest.FromBytes(newOpBytes)
	memo[*dag.opDigest] = newOpDigest
	def.Def = append(def.Def, newOpBytes)
	def.Metadata[newOpDigest] = *dag.metadata
	return def, newOpDigest, nil
}

func (dag *opDAG) AsExec() (*execOp, bool) {
	if dag.asExecOp != nil {
		return dag.asExecOp, true
	}
	pbExec := dag.GetExec()
	if pbExec == nil {
		return nil, false
	}
	exec := &execOp{
		opDAG:  dag,
		ExecOp: pbExec,
	}
	dag.asExecOp = exec
	return exec, true
}

// NOTE: only added execOp so far, but support for all other op types can be added as needed
type execOp struct {
	*opDAG
	*pb.ExecOp
}

func (exec *execOp) OutputMount() *pb.Mount {
	for _, mnt := range exec.Mounts {
		if mnt.Output == exec.outputIndex {
			return mnt
		}
	}
	// nil if mount is read-only or ForceNoOutput
	return nil
}

func (exec *execOp) OutputMountBase() *opDAG {
	if outputMount := exec.OutputMount(); outputMount != nil {
		// -1 indicates the input is scratch (i.e. it starts empty)
		if outputMount.Input != -1 {
			return exec.inputs[outputMount.Input]
		}
	}
	return nil
}
