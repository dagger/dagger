package llbtodagger

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

func (c *converter) convertMerge(op *buildkit.MergeOp) (*call.ID, error) {
	if op == nil {
		return nil, unsupported(opDigest(op.OpDAG), "merge", "missing merge op")
	}
	if len(op.OpDAG.Inputs) == 0 {
		return scratchDirectoryID(), nil
	}

	firstInputID, err := c.convertOp(op.OpDAG.Inputs[0])
	if err != nil {
		return nil, err
	}
	id, err := asDirectoryID(opDigest(op.OpDAG), "merge", firstInputID)
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
		nextID, err := asDirectoryID(opDigest(op.OpDAG), "merge", nextInputID)
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
		return nil, unsupported(opDigest(op.OpDAG), "diff", "missing diff op")
	}

	lowerInputID, err := c.resolveDiffInput(op.OpDAG, op.Lower.Input)
	if err != nil {
		return nil, err
	}
	upperInputID, err := c.resolveDiffInput(op.OpDAG, op.Upper.Input)
	if err != nil {
		return nil, err
	}
	lowerID, err := asDirectoryID(opDigest(op.OpDAG), "diff", lowerInputID)
	if err != nil {
		return nil, err
	}
	upperID, err := asDirectoryID(opDigest(op.OpDAG), "diff", upperInputID)
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
		return nil, unsupported(opDigest(dag), "diff", fmt.Sprintf("input index %d out of range", idx))
	}
	return c.convertOp(dag.Inputs[i])
}

func (c *converter) convertFile(op *buildkit.FileOp) (*call.ID, error) {
	if op == nil || op.FileOp == nil {
		return nil, unsupported(opDigest(op.OpDAG), "file", "missing file op")
	}

	inputIDs := make([]*call.ID, len(op.OpDAG.Inputs))
	inputContainerIDs := make([]*call.ID, len(op.OpDAG.Inputs))
	for i, in := range op.OpDAG.Inputs {
		id, err := c.convertOp(in)
		if err != nil {
			return nil, err
		}
		dirID, err := asDirectoryID(opDigest(op.OpDAG), "file", id)
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
		baseID, err := resolveFileActionInput(op.OpDAG, action.Input, inputIDs, actionOutputs)
		if err != nil {
			return nil, err
		}
		baseContainerID, err := resolveFileActionInputContainer(op.OpDAG, action.Input, inputContainerIDs, actionOutputContainers, actionOutputResolved)
		if err != nil {
			return nil, err
		}

		nextID, nextContainerID, err := c.applyFileAction(op.OpDAG, baseID, baseContainerID, action, inputIDs, actionOutputs)
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
		return nil, unsupported(opDigest(op.OpDAG), "file", fmt.Sprintf("no output for index %d", op.OutputIndex()))
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
	dag *buildkit.OpDAG,
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
		return nil, unsupported(opDigest(dag), "file", fmt.Sprintf("action input index %d out of range", idx))
	}
	if actionOutputs[rel] == nil {
		return nil, unsupported(opDigest(dag), "file", fmt.Sprintf("action input %d references unresolved action output", idx))
	}
	return actionOutputs[rel], nil
}

func resolveFileActionInputContainer(
	dag *buildkit.OpDAG,
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
		return nil, unsupported(opDigest(dag), "file", fmt.Sprintf("action input index %d out of range", idx))
	}
	if !actionOutputResolved[rel] {
		return nil, unsupported(opDigest(dag), "file", fmt.Sprintf("action input %d references unresolved action output", idx))
	}
	return actionOutputContainers[rel], nil
}

func (c *converter) applyFileAction(
	dag *buildkit.OpDAG,
	baseID *call.ID,
	baseContainerID *call.ID,
	action *pb.FileAction,
	opInputIDs []*call.ID,
	actionOutputs []*call.ID,
) (*call.ID, *call.ID, error) {
	if baseID.Type().NamedType() != directoryType().NamedType {
		return nil, nil, unsupported(opDigest(dag), "file", fmt.Sprintf("primary input type %q is not Directory", baseID.Type().NamedType()))
	}

	switch x := action.Action.(type) {
	case *pb.FileAction_Mkdir:
		nextID, err := applyMkdir(opDigest(dag), baseID, x.Mkdir)
		return nextID, baseContainerID, err
	case *pb.FileAction_Mkfile:
		nextID, err := applyMkfile(opDigest(dag), baseID, x.Mkfile)
		return nextID, baseContainerID, err
	case *pb.FileAction_Rm:
		nextID, err := applyRm(opDigest(dag), baseID, x.Rm)
		return nextID, baseContainerID, err
	case *pb.FileAction_Copy:
		srcID, err := resolveFileActionInput(dag, action.SecondaryInput, opInputIDs, actionOutputs)
		if err != nil {
			return nil, nil, err
		}
		if srcID.Type().NamedType() != directoryType().NamedType {
			return nil, nil, unsupported(opDigest(dag), "file", fmt.Sprintf("copy source type %q is not Directory", srcID.Type().NamedType()))
		}
		return applyCopy(opDigest(dag), baseID, baseContainerID, srcID, x.Copy)
	default:
		return nil, nil, unsupported(opDigest(dag), "file", "unsupported file action")
	}
}

func applyMkdir(opDgst digest.Digest, baseID *call.ID, mkdir *pb.FileActionMkDir) (*call.ID, error) {
	if mkdir == nil {
		return nil, unsupported(opDgst, "file.mkdir", "missing mkdir action")
	}
	if !mkdir.MakeParents {
		return nil, unsupported(opDgst, "file.mkdir", "mkdir without makeParents is unsupported")
	}
	if mkdir.Timestamp >= 0 {
		return nil, unsupported(opDgst, "file.mkdir", "mkdir timestamp override is unsupported")
	}

	id := appendCall(
		baseID,
		directoryType(),
		"withNewDirectory",
		argString("path", cleanPath(mkdir.Path)),
		argInt("permissions", int64(mkdir.Mode)),
	)

	owner, err := chownOwnerString(mkdir.Owner)
	if err != nil {
		return nil, unsupported(opDgst, "file.mkdir", err.Error())
	}
	if owner != "" {
		if ownerRequiresContainerResolution(owner) {
			return nil, unsupported(opDgst, "file.mkdir", "named user/group chown is unsupported for mkdir")
		}
		id = appendCall(
			id,
			directoryType(),
			"chown",
			argString("path", cleanPath(mkdir.Path)),
			argString("owner", owner),
		)
	}

	return id, nil
}

func applyMkfile(opDgst digest.Digest, baseID *call.ID, mkfile *pb.FileActionMkFile) (*call.ID, error) {
	if mkfile == nil {
		return nil, unsupported(opDgst, "file.mkfile", "missing mkfile action")
	}
	if mkfile.Timestamp >= 0 {
		return nil, unsupported(opDgst, "file.mkfile", "mkfile timestamp override is unsupported")
	}
	if !utf8.Valid(mkfile.Data) {
		return nil, unsupported(opDgst, "file.mkfile", "mkfile binary data is unsupported")
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
		return nil, unsupported(opDgst, "file.mkfile", err.Error())
	}
	if owner != "" {
		if ownerRequiresContainerResolution(owner) {
			return nil, unsupported(opDgst, "file.mkfile", "named user/group chown is unsupported for mkfile")
		}
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

func applyRm(opDgst digest.Digest, baseID *call.ID, rm *pb.FileActionRm) (*call.ID, error) {
	if rm == nil {
		return nil, unsupported(opDgst, "file.rm", "missing rm action")
	}
	return appendCall(baseID, directoryType(), "withoutFile", argString("path", cleanPath(rm.Path))), nil
}

func applyCopy(
	opDgst digest.Digest,
	baseID *call.ID,
	baseContainerID *call.ID,
	sourceID *call.ID,
	cp *pb.FileActionCopy,
) (*call.ID, *call.ID, error) {
	if cp == nil {
		return nil, nil, unsupported(opDgst, "file.copy", "missing copy action")
	}
	if cp.AttemptUnpackDockerCompatibility {
		return nil, nil, unsupported(opDgst, "file.copy", "archive auto-unpack is unsupported")
	}
	if cp.AlwaysReplaceExistingDestPaths {
		return nil, nil, unsupported(opDgst, "file.copy", "alwaysReplaceExistingDestPaths is unsupported")
	}

	sourceSubdir, include := deriveCopySelection(cp)
	sourceDirID := sourceID
	if sourceSubdir != "/" {
		sourceDirID = appendCall(sourceID, directoryType(), "directory", argString("path", sourceSubdir))
	}

	owner, err := chownOwnerString(cp.Owner)
	if err != nil {
		return nil, nil, unsupported(opDgst, "file.copy", err.Error())
	}
	if ownerRequiresContainerResolution(owner) {
		if baseContainerID == nil {
			return nil, nil, unsupported(opDgst, "file.copy", "named user/group chown requires container context")
		}
		id, ctrID := applyCopyViaContainer(baseID, baseContainerID, sourceDirID, cp, include, owner)
		return id, ctrID, nil
	}

	if filePath, ok := explicitFileCopyPath(cp, include); ok {
		fileID := appendCall(sourceDirID, fileType(), "file", argString("path", filePath))
		args := []*call.Argument{
			argString("path", cleanPath(cp.Dest)),
			argID("source", fileID),
		}
		if !cp.CreateDestPath {
			args = append(args, argBool("doNotCreateDestPath", true))
		}
		if owner != "" {
			args = append(args, argString("owner", owner))
		}
		if cp.Mode >= 0 {
			args = append(args, argInt("permissions", int64(cp.Mode)))
		}
		return appendCall(baseID, directoryType(), "withFile", args...), baseContainerID, nil
	}

	args := []*call.Argument{
		argString("path", cleanPath(cp.Dest)),
		argID("source", sourceDirID),
	}
	if !cp.CreateDestPath {
		args = append(args, argBool("doNotCreateDestPath", true))
	}
	if len(include) > 0 {
		args = append(args, argStringList("include", include))
	}
	if len(cp.ExcludePatterns) > 0 {
		args = append(args, argStringList("exclude", cp.ExcludePatterns))
	}

	if owner != "" {
		args = append(args, argString("owner", owner))
	}
	if cp.Mode >= 0 {
		args = append(args, argInt("permissions", int64(cp.Mode)))
	}

	return appendCall(baseID, directoryType(), "withDirectory", args...), baseContainerID, nil
}

func applyCopyViaContainer(
	baseID *call.ID,
	baseContainerID *call.ID,
	sourceDirID *call.ID,
	cp *pb.FileActionCopy,
	include []string,
	owner string,
) (*call.ID, *call.ID) {
	// Ensure the container context used for owner name resolution matches the
	// current directory view for this action input.
	workingContainerID := appendCall(
		baseContainerID,
		containerType(),
		"withRootfs",
		argID("directory", baseID),
	)

	var nextContainerID *call.ID
	if filePath, ok := explicitFileCopyPath(cp, include); ok {
		fileID := appendCall(sourceDirID, fileType(), "file", argString("path", filePath))
		args := []*call.Argument{
			argString("path", cleanPath(cp.Dest)),
			argID("source", fileID),
			argString("owner", owner),
		}
		if !cp.CreateDestPath {
			args = append(args, argBool("doNotCreateDestPath", true))
		}
		if cp.Mode >= 0 {
			args = append(args, argInt("permissions", int64(cp.Mode)))
		}
		nextContainerID = appendCall(workingContainerID, containerType(), "withFile", args...)
	} else {
		args := []*call.Argument{
			argString("path", cleanPath(cp.Dest)),
			argID("source", sourceDirID),
			argString("owner", owner),
		}
		if !cp.CreateDestPath {
			args = append(args, argBool("doNotCreateDestPath", true))
		}
		if len(include) > 0 {
			args = append(args, argStringList("include", include))
		}
		if len(cp.ExcludePatterns) > 0 {
			args = append(args, argStringList("exclude", cp.ExcludePatterns))
		}
		if cp.Mode >= 0 {
			args = append(args, argInt("permissions", int64(cp.Mode)))
		}
		nextContainerID = appendCall(workingContainerID, containerType(), "withDirectory", args...)
	}

	return appendCall(nextContainerID, directoryType(), "rootfs"), nextContainerID
}

func deriveCopySelection(cp *pb.FileActionCopy) (sourceSubdir string, include []string) {
	src := cleanPath(cp.Src)
	include = append([]string{}, cp.IncludePatterns...)

	switch {
	case cp.AllowWildcard:
		sourceSubdir = cleanPath(path.Dir(src))
		pattern := path.Base(src)
		if pattern != "." && pattern != "/" {
			include = append([]string{pattern}, include...)
		}
	case cp.DirCopyContents:
		sourceSubdir = src
	case src == "/" || src == ".":
		sourceSubdir = "/"
	default:
		sourceSubdir = cleanPath(path.Dir(src))
		item := path.Base(src)
		if item != "." && item != "/" {
			include = append([]string{item}, include...)
		}
	}

	if sourceSubdir == "" {
		sourceSubdir = "/"
	}
	return sourceSubdir, include
}

func explicitFileCopyPath(cp *pb.FileActionCopy, include []string) (string, bool) {
	if cp == nil {
		return "", false
	}
	if strings.HasSuffix(cp.Dest, "/") || strings.HasSuffix(cp.Dest, "/.") {
		return "", false
	}
	if len(cp.IncludePatterns) > 0 || len(cp.ExcludePatterns) > 0 {
		return "", false
	}
	if len(include) != 1 {
		return "", false
	}
	inc := path.Clean(include[0])
	if inc == "." || inc == "/" || strings.HasPrefix(inc, "../") {
		return "", false
	}
	if hasPathWildcard(inc) {
		return "", false
	}
	srcBase := path.Base(cleanPath(cp.Src))
	if srcBase == "." || srcBase == "/" || hasPathWildcard(srcBase) {
		return "", false
	}
	return strings.TrimPrefix(inc, "/"), true
}

func hasPathWildcard(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

func chownOwnerString(chown *pb.ChownOpt) (string, error) {
	if chown == nil {
		return "", nil
	}
	var (
		user string
		err  error
	)
	// BuildKit represents group-only chown from Dockerfile ("--chown=:gid")
	// as an empty named user with group set. Normalize that user side to UID 0.
	if isEmptyNamedUser(chown.User) && chown.Group != nil {
		user = ""
	} else {
		user, err = userOptToString(chown.User)
		if err != nil {
			return "", err
		}
	}

	group, err := userOptToString(chown.Group)
	if err != nil {
		return "", err
	}

	switch {
	case user == "" && group == "":
		return "", nil
	case user == "" && group != "":
		return "0:" + group, nil
	case group == "":
		return user, nil
	default:
		return user + ":" + group, nil
	}
}

func isEmptyNamedUser(user *pb.UserOpt) bool {
	if user == nil {
		return false
	}
	byName, ok := user.User.(*pb.UserOpt_ByName)
	if !ok {
		return false
	}
	if byName.ByName == nil {
		return false
	}
	return strings.TrimSpace(byName.ByName.Name) == ""
}

func userOptToString(user *pb.UserOpt) (string, error) {
	if user == nil {
		return "", nil
	}
	switch v := user.User.(type) {
	case *pb.UserOpt_ByID:
		return strconv.FormatUint(uint64(v.ByID), 10), nil
	case *pb.UserOpt_ByName:
		if v.ByName == nil || strings.TrimSpace(v.ByName.Name) == "" {
			return "", fmt.Errorf("empty named user is unsupported")
		}
		return strings.TrimSpace(v.ByName.Name), nil
	default:
		return "", fmt.Errorf("unknown user option type")
	}
}

func ownerRequiresContainerResolution(owner string) bool {
	if owner == "" {
		return false
	}
	user, group, hasGroup := strings.Cut(owner, ":")
	if ownerComponentRequiresResolution(user) {
		return true
	}
	if hasGroup && ownerComponentRequiresResolution(group) {
		return true
	}
	return false
}

func ownerComponentRequiresResolution(component string) bool {
	if component == "" {
		return false
	}
	_, err := strconv.ParseUint(component, 10, 32)
	return err != nil
}
