package windows

// Constants for well-known SIDs in the Windows container.
// These are currently undocumented.
// See https://github.com/moby/buildkit/pull/5791#discussion_r1976652227 for more information.
const (
	ContainerAdministratorSidString = "S-1-5-93-2-1"
	ContainerUserSidString          = "S-1-5-93-2-2"
)
