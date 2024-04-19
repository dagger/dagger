package sshprovider_test

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/cmd/buildctl/build"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
)

func TestToAgentSource(t *testing.T) {
	configs, err := build.ParseSSH([]string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sshprovider.NewSSHAgentProvider(configs)
	ok := err == nil || strings.Contains(err.Error(), "invalid empty ssh agent socket")
	if !ok {
		t.Fatal(err)
	}
}
