package buildkit

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	srctypes "github.com/moby/buildkit/source/types"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"github.com/dagger/dagger/engine/sources/blob"
)

func DefToDAG(def *pb.Definition) (*OpDAG, error) {
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
		map[digest.Digest]*OpDAG{},
	)
}

func opToDAG(
	op *pb.Op,
	opDigest digest.Digest,
	outputIndex pb.OutputIndex,
	digestToOp map[digest.Digest]*pb.Op,
	digestToMetadata map[digest.Digest]*pb.OpMetadata,
	memo map[digest.Digest]*OpDAG,
) (*OpDAG, error) {
	if opDigest == "" {
		return nil, fmt.Errorf("unexpected empty op digest")
	}
	if dag, ok := memo[opDigest]; ok {
		outputSpecificDAG, ok := dag.allOutputs[outputIndex]
		if !ok {
			outputSpecificDAG = &OpDAG{
				Op:          dag.Op,
				OpDigest:    dag.OpDigest,
				Metadata:    dag.Metadata,
				Inputs:      dag.Inputs,
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
	dag := &OpDAG{
		Op:          op,
		OpDigest:    &opDigest,
		Metadata:    digestToMetadata[opDigest],
		outputIndex: outputIndex,
		allOutputs:  map[pb.OutputIndex]*OpDAG{},
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
		dag.Inputs = append(dag.Inputs, inputDAG)
	}
	return dag, nil
}

type OpDAG struct {
	*pb.Op                  // the root of the DAG
	OpDigest *digest.Digest // the digest of this root, common across all outputIndexes for this root
	Metadata *pb.OpMetadata // metadata for the root
	Inputs   []*OpDAG       // the inputs to the root

	outputIndex pb.OutputIndex            // the specific output of the op that the root represents
	allOutputs  map[pb.OutputIndex]*OpDAG // all outputs of this root, including this one

	// cached op conversions
	asExecOp  *ExecOp
	asFileOp  *FileOp
	asMergeOp *MergeOp
	asDiffOp  *DiffOp
	asImageOp *ImageOp
	asGitOp   *GitOp
	asLocalOp *LocalOp
	asHTTPOp  *HTTPOp
	asOCIOp   *OCIOp
	asBlobOp  *BlobOp
}

func (dag *OpDAG) String() string {
	builder := &strings.Builder{}
	return dag.toString(builder, "")
}

func (dag *OpDAG) toString(builder *strings.Builder, indent string) string {
	fmt.Fprintf(builder, "%s%d %+v\n", indent, dag.outputIndex, dag.Op.Op)
	for _, input := range dag.Inputs {
		input.toString(builder, indent+"  ")
	}
	return builder.String()
}

func (dag *OpDAG) Walk(f func(*OpDAG) error) error {
	return dag.walk(f, map[*OpDAG]struct{}{})
}

func (dag *OpDAG) walk(f func(*OpDAG) error, memo map[*OpDAG]struct{}) error {
	if _, ok := memo[dag]; ok {
		return nil
	}
	memo[dag] = struct{}{}

	err := f(dag)
	if err == SkipInputs {
		return nil
	}
	if err != nil {
		return err
	}

	for _, input := range dag.Inputs {
		if err := input.walk(f, memo); err != nil {
			return err
		}
	}
	return nil
}

var SkipInputs = fmt.Errorf("skip inputs") //nolint:stylecheck // Err prefix isn't convention for Walk control errors

// Marshal will convert the dag back to a flat pb.Definition, updating all digests
// based on any modifications made to the dag.
func (dag *OpDAG) Marshal() (*pb.Definition, error) {
	def, _, err := dag.marshal(&pb.Definition{
		Metadata: map[digest.Digest]pb.OpMetadata{},
	}, map[digest.Digest]digest.Digest{})
	if dag.Op.Op != nil {
		op := &pb.Op{
			Inputs: []*pb.Input{
				{Digest: *dag.OpDigest, Index: dag.outputIndex},
			},
			Platform:    dag.Platform,
			Constraints: dag.Constraints,
		}
		dt, err := op.Marshal()
		if err != nil {
			return nil, err
		}
		dig := digest.FromBytes(dt)
		def.Def = append(def.Def, dt)
		def.Metadata[dig] = *dag.Metadata
	}
	return def, err
}

func (dag *OpDAG) marshal(def *pb.Definition, memo map[digest.Digest]digest.Digest) (*pb.Definition, digest.Digest, error) {
	if dgst, ok := memo[*dag.OpDigest]; ok {
		return def, dgst, nil
	}

	newOp := &pb.Op{
		Op:          dag.Op.Op,
		Platform:    dag.Platform,
		Constraints: dag.Constraints,
	}
	for _, input := range dag.Inputs {
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
	memo[*dag.OpDigest] = newOpDigest
	def.Def = append(def.Def, newOpBytes)
	def.Metadata[newOpDigest] = *dag.Metadata
	return def, newOpDigest, nil
}

func (dag *OpDAG) BlobDependencies() (map[digest.Digest]*ocispecs.Descriptor, error) {
	dependencyBlobs := map[digest.Digest]*ocispecs.Descriptor{}
	if err := dag.Walk(func(dag *OpDAG) error {
		blobOp, ok := dag.AsBlob()
		if !ok {
			return nil
		}
		desc, err := blobOp.OCIDescriptor()
		if err != nil {
			return fmt.Errorf("failed to get blob descriptor: %w", err)
		}
		dependencyBlobs[desc.Digest] = &desc
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk pb definition dag: %w", err)
	}
	return dependencyBlobs, nil
}

type ExecOp struct {
	*OpDAG
	*pb.ExecOp
}

func (dag *OpDAG) AsExec() (*ExecOp, bool) {
	if dag.asExecOp != nil {
		return dag.asExecOp, true
	}
	pbExec := dag.GetExec()
	if pbExec == nil {
		return nil, false
	}
	exec := &ExecOp{
		OpDAG:  dag,
		ExecOp: pbExec,
	}
	dag.asExecOp = exec
	return exec, true
}

func (exec *ExecOp) Input(i pb.InputIndex) *OpDAG {
	return exec.Inputs[i]
}

func (exec *ExecOp) OutputMount() *pb.Mount {
	for _, mnt := range exec.Mounts {
		if mnt.Output == exec.outputIndex {
			return mnt
		}
	}
	// nil if mount is read-only or ForceNoOutput
	return nil
}

func (exec *ExecOp) OutputMountBase() *OpDAG {
	if outputMount := exec.OutputMount(); outputMount != nil {
		// -1 indicates the input is scratch (i.e. it starts empty)
		if outputMount.Input != -1 {
			return exec.Inputs[outputMount.Input]
		}
	}
	return nil
}

type FileOp struct {
	*OpDAG
	*pb.FileOp
}

func (dag *OpDAG) AsFile() (*FileOp, bool) {
	if dag.asFileOp != nil {
		return dag.asFileOp, true
	}
	pbFile := dag.GetFile()
	if pbFile == nil {
		return nil, false
	}
	file := &FileOp{
		OpDAG:  dag,
		FileOp: pbFile,
	}
	dag.asFileOp = file
	return file, true
}

type MergeOp struct {
	*OpDAG
	*pb.MergeOp
}

func (dag *OpDAG) AsMerge() (*MergeOp, bool) {
	if dag.asMergeOp != nil {
		return dag.asMergeOp, true
	}
	pbMerge := dag.GetMerge()
	if pbMerge == nil {
		return nil, false
	}
	merge := &MergeOp{
		OpDAG:   dag,
		MergeOp: pbMerge,
	}
	dag.asMergeOp = merge
	return merge, true
}

type DiffOp struct {
	*OpDAG
	*pb.DiffOp
}

func (dag *OpDAG) AsDiff() (*DiffOp, bool) {
	if dag.asDiffOp != nil {
		return dag.asDiffOp, true
	}
	pbDiff := dag.GetDiff()
	if pbDiff == nil {
		return nil, false
	}
	diff := &DiffOp{
		OpDAG:  dag,
		DiffOp: pbDiff,
	}
	dag.asDiffOp = diff
	return diff, true
}

type ImageOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsImage() (*ImageOp, bool) {
	if dag.asImageOp != nil {
		return dag.asImageOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	if !strings.HasPrefix(pbSource.Identifier, srctypes.DockerImageScheme+"://") {
		return nil, false
	}
	img := &ImageOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asImageOp = img
	return img, true
}

type GitOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsGit() (*GitOp, bool) {
	if dag.asGitOp != nil {
		return dag.asGitOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	if !strings.HasPrefix(pbSource.Identifier, srctypes.GitScheme+"://") {
		return nil, false
	}
	op := &GitOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asGitOp = op
	return op, true
}

type LocalOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsLocal() (*LocalOp, bool) {
	if dag.asLocalOp != nil {
		return dag.asLocalOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	if !strings.HasPrefix(pbSource.Identifier, srctypes.LocalScheme+"://") {
		return nil, false
	}
	op := &LocalOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asLocalOp = op
	return op, true
}

type HTTPOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsHTTP() (*HTTPOp, bool) {
	if dag.asHTTPOp != nil {
		return dag.asHTTPOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	hasHTTPScheme := strings.HasPrefix(pbSource.Identifier, srctypes.HTTPScheme+"://")
	hasHTTPSScheme := strings.HasPrefix(pbSource.Identifier, srctypes.HTTPSScheme+"://")
	if !hasHTTPScheme && !hasHTTPSScheme {
		return nil, false
	}
	op := &HTTPOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asHTTPOp = op
	return op, true
}

type OCIOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsOCI() (*OCIOp, bool) {
	if dag.asOCIOp != nil {
		return dag.asOCIOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	if !strings.HasPrefix(pbSource.Identifier, srctypes.OCIScheme+"://") {
		return nil, false
	}
	op := &OCIOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asOCIOp = op
	return op, true
}

type BlobOp struct {
	*OpDAG
	*pb.SourceOp
}

func (dag *OpDAG) AsBlob() (*BlobOp, bool) {
	if dag.asBlobOp != nil {
		return dag.asBlobOp, true
	}
	pbSource := dag.GetSource()
	if pbSource == nil {
		return nil, false
	}
	if !strings.HasPrefix(pbSource.Identifier, blob.BlobScheme+"://") {
		return nil, false
	}
	op := &BlobOp{
		OpDAG:    dag,
		SourceOp: pbSource,
	}
	dag.asBlobOp = op
	return op, true
}

func (op *BlobOp) OCIDescriptor() (ocispecs.Descriptor, error) {
	id, err := blob.IdentifierFromPB(op.SourceOp)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	return id.Descriptor, nil
}
