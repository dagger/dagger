package file

import (
	"github.com/dagger/dagger/internal/buildkit/util/windows"
	copy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/moby/sys/user"
)

func mapUserToChowner(user *copy.User, _ *user.IdentityMapping) (copy.Chowner, error) {
	if user == nil || user.SID == "" {
		return func(old *copy.User) (*copy.User, error) {
			if old == nil || old.SID == "" {
				old = &copy.User{
					SID: windows.ContainerAdministratorSidString,
				}
			}
			return old, nil
		}, nil
	}
	return func(*copy.User) (*copy.User, error) {
		return user, nil
	}, nil
}
