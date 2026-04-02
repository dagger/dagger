package llbtodagger

import (
	"fmt"
	"unicode/utf8"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
)

func (c *converter) convertMerge(op *buildkit.MergeOp) (*call.ID, error) {
	if op == nil {
		return nil, fmt.Errorf("missing merge op")
	}
	if len(op.OpDAG.Inputs) == 0 {
		return scratchDirectoryID(), nil
	}

	firstInputID, err := c.convertOp(op.OpDAG.Inputs[0])
	if err != nil {
		return nil, err
	}
	id, err := asDirectoryID(firstInputID)
	if err != nil {
		return nil, err
	}
	var baseContainer *call.ID
	if firstInputID != nil && firstInputID.Type().NamedType() == containerType().NamedType {
		baseContainer = firstInputID
	}

	for i := 1; i < len(op.OpDAG.Inputs); i++ {
		nextInputID, err := c.convertOp(op.OpDAG.Inputs[i])
		if err != nil {
			return nil, err
		}
		nextID, err := asDirectoryID(nextInputID)
		if err != nil {
			return nil, err
		}
		id = appendCall(
			id,
			directoryType(),
			"withDirectory",
			argString("path", "/"),
			argID("source", nextID),
		)
	}

	if baseContainer != nil {
		return appendCall(baseContainer, containerType(), "withRootfs", argID("directory", id)), nil
	}
	return id, nil
}

func (c *converter) convertDiff(op *buildkit.DiffOp) (*call.ID, error) {
	if op == nil || op.DiffOp == nil {
		return nil, fmt.Errorf("missing diff op")
	}

	lowerInputID, err := c.resolveDiffInput(op.OpDAG, op.Lower.Input)
	if err != nil {
		return nil, err
	}
	upperInputID, err := c.resolveDiffInput(op.OpDAG, op.Upper.Input)
	if err != nil {
		return nil, err
	}
	lowerID, err := asDirectoryID(lowerInputID)
	if err != nil {
		return nil, err
	}
	upperID, err := asDirectoryID(upperInputID)
	if err != nil {
		return nil, err
	}

	diffID := appendCall(upperID, directoryType(), "diff", argID("other", lowerID))
	if upperInputID != nil && upperInputID.Type().NamedType() == containerType().NamedType {
		return appendCall(upperInputID, containerType(), "withRootfs", argID("directory", diffID)), nil
	}
	return diffID, nil
}

func (c *converter) resolveDiffInput(dag *buildkit.OpDAG, idx pb.InputIndex) (*call.ID, error) {
	if idx == pb.Empty {
		return scratchDirectoryID(), nil
	}
	i := int(idx)
	if i < 0 || i >= len(dag.Inputs) {
		return nil, fmt.Errorf("input index %d out of range", idx)
	}
	return c.convertOp(dag.Inputs[i])
}

func (c *converter) convertFile(op *buildkit.FileOp) (*call.ID, error) {
	if op == nil || op.FileOp == nil {
		return nil, fmt.Errorf("missing file op")
	}

	inputIDs := make([]*call.ID, len(op.OpDAG.Inputs))
	inputContainerIDs := make([]*call.ID, len(op.OpDAG.Inputs))
	for i, in := range op.OpDAG.Inputs {
		id, err := c.convertOp(in)
		if err != nil {
			return nil, err
		}
		dirID, err := asDirectoryID(id)
		if err != nil {
			return nil, err
		}
		inputIDs[i] = dirID
		if id != nil && id.Type().NamedType() == containerType().NamedType {
			inputContainerIDs[i] = id
		}
	}

	actionOutputs := make([]*call.ID, len(op.Actions))
	actionOutputContainers := make([]*call.ID, len(op.Actions))
	actionOutputResolved := make([]bool, len(op.Actions))
	outputIDs := map[pb.OutputIndex]*call.ID{}
	outputContainers := map[pb.OutputIndex]*call.ID{}

	for i, action := range op.Actions {
		baseID, err := resolveFileActionInput(action.Input, inputIDs, actionOutputs)
		if err != nil {
			return nil, err
		}
		baseContainerID, err := resolveFileActionInputContainer(action.Input, inputContainerIDs, actionOutputContainers, actionOutputResolved)
		if err != nil {
			return nil, err
		}

		nextID, nextContainerID, err := c.applyFileAction(baseID, baseContainerID, action, inputIDs, actionOutputs)
		if err != nil {
			return nil, err
		}
		actionOutputs[i] = nextID
		actionOutputContainers[i] = nextContainerID
		actionOutputResolved[i] = true

		if action.Output != pb.SkipOutput {
			outputIDs[action.Output] = nextID
			outputContainers[action.Output] = nextContainerID
		}
	}

	outID, ok := outputIDs[op.OutputIndex()]
	if !ok {
		return nil, fmt.Errorf("no output for index %d", op.OutputIndex())
	}
	if baseContainer := outputContainers[op.OutputIndex()]; baseContainer != nil {
		if outID != nil && outID.Field() == "rootfs" && outID.Receiver() == baseContainer {
			return baseContainer, nil
		}
		return appendCall(baseContainer, containerType(), "withRootfs", argID("directory", outID)), nil
	}
	return outID, nil
}

func resolveFileActionInput(
	idx pb.InputIndex,
	opInputIDs []*call.ID,
	actionOutputs []*call.ID,
) (*call.ID, error) {
	if idx == pb.Empty {
		return scratchDirectoryID(), nil
	}

	i := int(idx)
	if i >= 0 && i < len(opInputIDs) {
		return opInputIDs[i], nil
	}

	rel := i - len(opInputIDs)
	if rel < 0 || rel >= len(actionOutputs) {
		return nil, fmt.Errorf("action input index %d out of range", idx)
	}
	if actionOutputs[rel] == nil {
		return nil, fmt.Errorf("action input %d references unresolved action output", idx)
	}
	return actionOutputs[rel], nil
}

func resolveFileActionInputContainer(
	idx pb.InputIndex,
	opInputContainers []*call.ID,
	actionOutputContainers []*call.ID,
	actionOutputResolved []bool,
) (*call.ID, error) {
	if idx == pb.Empty {
		return nil, nil
	}

	i := int(idx)
	if i >= 0 && i < len(opInputContainers) {
		return opInputContainers[i], nil
	}

	rel := i - len(opInputContainers)
	if rel < 0 || rel >= len(actionOutputContainers) {
		return nil, fmt.Errorf("action input index %d out of range", idx)
	}
	if !actionOutputResolved[rel] {
		return nil, fmt.Errorf("action input %d references unresolved action output", idx)
	}
	return actionOutputContainers[rel], nil
}

func (c *converter) applyFileAction(
	baseID *call.ID,
	baseContainerID *call.ID,
	action *pb.FileAction,
	opInputIDs []*call.ID,
	actionOutputs []*call.ID,
) (*call.ID, *call.ID, error) {
	if baseID.Type().NamedType() != directoryType().NamedType {
		return nil, nil, fmt.Errorf("primary input type %q is not Directory", baseID.Type().NamedType())
	}

	switch x := action.Action.(type) {
	case *pb.FileAction_Mkdir:
		return applyMkdir(baseID, baseContainerID, x.Mkdir)
	case *pb.FileAction_Mkfile:
		nextID, err := applyMkfile(baseID, x.Mkfile)
		return nextID, baseContainerID, err
	case *pb.FileAction_Rm:
		nextID, err := applyRm(baseID, x.Rm)
		return nextID, baseContainerID, err
	case *pb.FileAction_Copy:
		srcID, err := resolveFileActionInput(action.SecondaryInput, opInputIDs, actionOutputs)
		if err != nil {
			return nil, nil, err
		}
		if srcID.Type().NamedType() != directoryType().NamedType {
			return nil, nil, fmt.Errorf("copy source type %q is not Directory", srcID.Type().NamedType())
		}
		return applyCopy(baseID, baseContainerID, srcID, x.Copy)
	default:
		return nil, nil, fmt.Errorf("unsupported file action")
	}
}

const mkdirCompatSyntheticSourcePath = "/.__llbtodagger_mkdir__"

func applyMkdir(baseID *call.ID, baseContainerID *call.ID, mkdir *pb.FileActionMkDir) (*call.ID, *call.ID, error) {
	if mkdir == nil {
		return nil, nil, fmt.Errorf("missing mkdir action")
	}
	if !mkdir.MakeParents {
		return nil, nil, fmt.Errorf("mkdir without makeParents is unsupported")
	}
	if mkdir.Timestamp >= 0 {
		return nil, nil, fmt.Errorf("mkdir timestamp override is unsupported")
	}

	mkdirPath := cleanPath(mkdir.Path)

	owner, err := chownOwnerString(mkdir.Owner)
	if err != nil {
		return nil, nil, err
	}

	if owner != "" {
		if baseContainerID == nil {
			return nil, nil, fmt.Errorf("named user/group chown requires container context")
		}

		workingContainerID := appendCall(
			baseContainerID,
			containerType(),
			"withRootfs",
			argID("directory", baseID),
		)

		mkdirSource := appendCall(
			appendCall(
				scratchDirectoryID(),
				directoryType(),
				"withNewDirectory",
				argString("path", mkdirCompatSyntheticSourcePath),
				argInt("permissions", int64(mkdir.Mode)),
			),
			directoryType(),
			"directory",
			argString("path", mkdirCompatSyntheticSourcePath),
		)

		nextContainerID := appendCall(
			workingContainerID,
			containerType(),
			"withDirectory",
			argString("path", mkdirPath),
			argID("source", mkdirSource),
			argString("owner", owner),
		)
		return appendCall(nextContainerID, directoryType(), "rootfs"), nextContainerID, nil
	}

	id := appendCall(
		baseID,
		directoryType(),
		"withNewDirectory",
		argString("path", mkdirPath),
		argInt("permissions", int64(mkdir.Mode)),
	)

	if owner != "" {
		id = appendCall(
			id,
			directoryType(),
			"chown",
			argString("path", mkdirPath),
			argString("owner", owner),
		)
	}

	return id, baseContainerID, nil
}

func applyMkfile(baseID *call.ID, mkfile *pb.FileActionMkFile) (*call.ID, error) {
	if mkfile == nil {
		return nil, fmt.Errorf("missing mkfile action")
	}
	if mkfile.Timestamp >= 0 {
		return nil, fmt.Errorf("mkfile timestamp override is unsupported")
	}
	if !utf8.Valid(mkfile.Data) {
		return nil, fmt.Errorf("mkfile binary data is unsupported")
	}

	filePath := cleanPath(mkfile.Path)
	id := appendCall(
		baseID,
		directoryType(),
		"withNewFile",
		argString("path", filePath),
		argString("contents", string(mkfile.Data)),
		argInt("permissions", int64(mkfile.Mode)),
	)

	owner, err := chownOwnerString(mkfile.Owner)
	if err != nil {
		return nil, err
	}
	if owner != "" {
		id = appendCall(
			id,
			directoryType(),
			"chown",
			argString("path", filePath),
			argString("owner", owner),
		)
	}

	return id, nil
}

func applyRm(baseID *call.ID, rm *pb.FileActionRm) (*call.ID, error) {
	if rm == nil {
		return nil, fmt.Errorf("missing rm action")
	}
	return appendCall(baseID, directoryType(), "withoutFile", argString("path", cleanPath(rm.Path))), nil
}

func applyCopy(
	baseID *call.ID,
	baseContainerID *call.ID,
	sourceID *call.ID,
	cp *pb.FileActionCopy,
) (*call.ID, *call.ID, error) {
	if cp == nil {
		return nil, nil, fmt.Errorf("missing copy action")
	}
	if cp.AlwaysReplaceExistingDestPaths {
		return nil, nil, fmt.Errorf("alwaysReplaceExistingDestPaths is unsupported")
	}

	args := []*call.Argument{
		argString("srcPath", cleanPath(cp.Src)),
		argString("path", cleanPath(cp.Dest)),
		argID("source", sourceID),
	}

	if cp.FollowSymlink {
		args = append(args, argBool("followSymlink", true))
	}
	if cp.DirCopyContents {
		args = append(args, argBool("dirCopyContents", true))
	}
	if cp.AttemptUnpackDockerCompatibility {
		args = append(args, argBool("attemptUnpackDockerCompatibility", true))
	}
	if cp.CreateDestPath {
		args = append(args, argBool("createDestPath", true))
	}
	if cp.AllowWildcard {
		args = append(args, argBool("allowWildcard", true))
	}
	if cp.AllowEmptyWildcard {
		args = append(args, argBool("AllowEmptyWildcard", true))
	}
	if cp.Timestamp > 0 {
		panic("timestamp not supported")
	}
	if len(cp.IncludePatterns) > 0 {
		args = append(args, argStringList("include", cp.IncludePatterns))
	}
	if len(cp.ExcludePatterns) > 0 {
		args = append(args, argStringList("exclude", cp.ExcludePatterns))
	}
	if cp.AlwaysReplaceExistingDestPaths {
		args = append(args, argBool("alwaysReplaceExistingDestPaths", true))
	}

	owner, err := chownOwnerString(cp.Owner)
	if err != nil {
		return nil, nil, err
	}
	if owner != "" {
		args = append(args, argString("owner", owner))
	}
	if cp.Mode >= 0 {
		args = append(args, argInt("permissions", int64(cp.Mode)))
	}

	return appendCall(baseID, directoryType(), "__withDirectoryDockerfileCompat", args...), baseContainerID, nil
}

func chownUserOptString(uOpt *pb.UserOpt) (string, error) {
	switch x := uOpt.User.(type) {
	case *pb.UserOpt_ByName:
		if x != nil && x.ByName != nil {
			return x.ByName.Name, nil
		}
	case *pb.UserOpt_ByID:
		if x != nil {
			return fmt.Sprintf("%d", x.ByID), nil
		}
	default:
		return "", fmt.Errorf("unhandled type %T", uOpt.User)
	}
	return "", nil
}

func chownOwnerString(chown *pb.ChownOpt) (string, error) {
	if chown == nil {
		return "", nil
	}

	var (
		user  string
		group string
		err   error
	)

	if chown.User != nil {
		user, err = chownUserOptString(chown.User)
		if err != nil {
			return "", err
		}
	}
	if chown.Group != nil {
		group, err = chownUserOptString(chown.Group)
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s:%s", user, group), nil
}
