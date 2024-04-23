package main

import (
	"strings"

	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
)

func parseIdentityMapping(str string) (*idtools.IdentityMapping, error) {
	if str == "" {
		return nil, nil
	}

	idparts := strings.SplitN(str, ":", 3)
	if len(idparts) > 2 {
		return nil, errors.Errorf("invalid userns remap specification in %q", str)
	}

	username := idparts[0]

	bklog.L.Debugf("user namespaces: ID ranges will be mapped to subuid ranges of: %s", username)

	mappings, err := idtools.LoadIdentityMapping(username)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create ID mappings")
	}
	return &mappings, nil
}
