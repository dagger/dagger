package daggercmd

import (
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
)

func user(orgs ...string) *cloudapi.UserResponse {
	u := &cloudapi.UserResponse{}
	for _, name := range orgs {
		u.Orgs = append(u.Orgs, cloudauth.Org{Name: name})
	}
	return u
}

func TestUserOrgMembershipError(t *testing.T) {
	repo := "github.com/dagger/dagger"

	t.Run("member returns nil", func(t *testing.T) {
		require.NoError(t, userOrgMembershipError(user("acme", "dagger"), "dagger", repo))
	})

	t.Run("member match is case-insensitive", func(t *testing.T) {
		require.NoError(t, userOrgMembershipError(user("Dagger"), "dagger", repo))
	})

	t.Run("non-member with other orgs", func(t *testing.T) {
		err := userOrgMembershipError(user("acme", "widgets"), "dagger", repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), repo)
		require.Contains(t, err.Error(), `owned by Dagger Cloud organization "dagger"`)
		require.Contains(t, err.Error(), "not a member")
		require.Contains(t, err.Error(), "your organizations: acme, widgets")
	})

	t.Run("non-member with no orgs", func(t *testing.T) {
		err := userOrgMembershipError(user(), "dagger", repo)
		require.Error(t, err)
		require.Contains(t, err.Error(), `owned by Dagger Cloud organization "dagger"`)
		require.Contains(t, err.Error(), "not a member of any Dagger Cloud organizations")
	})
}
