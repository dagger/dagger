package file

import (
	copy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/docker/docker/pkg/idtools"
)

func mapUserToChowner(user *copy.User, _ *idtools.IdentityMapping) (copy.Chowner, error) {
	if user == nil || user.SID == "" {
		return func(old *copy.User) (*copy.User, error) {
			if old == nil || old.SID == "" {
				old = &copy.User{
					SID: idtools.ContainerAdministratorSidString,
				}
			}
			return old, nil
		}, nil
	}
	return func(*copy.User) (*copy.User, error) {
		return user, nil
	}, nil
}
