package apicaps

import (
	"testing"

	pb "github.com/moby/buildkit/util/apicaps/pb"
	"github.com/stretchr/testify/assert"
)

func TestDisabledCap(t *testing.T) {
	var cl CapList
	cl.Init(Cap{
		ID:      "cap1",
		Name:    "a test cap",
		Enabled: true,
		Status:  CapStatusExperimental,
	})
	cl.Init(Cap{
		ID:      "cap2",
		Name:    "a second test cap",
		Enabled: false,
		Status:  CapStatusExperimental,
	})

	cs := cl.CapSet([]pb.APICap{
		{ID: "cap1", Enabled: true},
		{ID: "cap2", Enabled: true},
	})
	err := cs.Supports("cap1")
	assert.NoError(t, err)
	err = cs.Supports("cap2")
	assert.NoError(t, err)

	cs = cl.CapSet([]pb.APICap{
		{ID: "cap1", Enabled: true},
		{ID: "cap2", Enabled: false},
	})
	err = cs.Supports("cap1")
	assert.NoError(t, err)
	err = cs.Supports("cap2")
	assert.EqualError(t, err, "requested experimental feature cap2 (a second test cap) has been disabled on the build server")

	cs = cl.CapSet([]pb.APICap{
		{ID: "cap1", Enabled: false},
		{ID: "cap2", Enabled: true},
	})
	err = cs.Supports("cap1")
	assert.EqualError(t, err, "requested experimental feature cap1 (a test cap) has been disabled on the build server")
	err = cs.Supports("cap2")
	assert.NoError(t, err)

	cs = cl.CapSet([]pb.APICap{
		{ID: "cap1", Enabled: false},
		{ID: "cap2", Enabled: false},
	})
	err = cs.Supports("cap1")
	assert.EqualError(t, err, "requested experimental feature cap1 (a test cap) has been disabled on the build server")
	err = cs.Supports("cap2")
	assert.EqualError(t, err, "requested experimental feature cap2 (a second test cap) has been disabled on the build server")
}
