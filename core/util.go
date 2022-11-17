package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sync"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
)

// encodeID JSON marshals and base64-encodes an arbitrary payload.
func encodeID(payload any) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)

	return string(b64Bytes), nil
}

// decodeID base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func decodeID[T ~string](payload any, id T) error {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(id)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(id))
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v", payload, err)
	}

	jsonBytes = jsonBytes[:n]

	return json.Unmarshal(jsonBytes, payload)
}

func absPath(workDir string, containerPath string) string {
	if path.IsAbs(containerPath) {
		return containerPath
	}

	if workDir == "" {
		workDir = "/"
	}

	return path.Join(workDir, containerPath)
}

func defToState(def *pb.Definition) (llb.State, error) {
	if def.Def == nil {
		// NB(vito): llb.Scratch().Marshal().ToPB() produces an empty
		// *pb.Definition. If we don't convert it properly back to a llb.Scratch()
		// we'll hit 'cannot marshal empty definition op' when trying to marshal it
		// again.
		return llb.Scratch(), nil
	}

	defop, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}

	return llb.NewState(defop), nil
}

// mirrorCh mirrors messages from one channel to another, protecting the
// destination channel from being closed.
//
// this is used to reflect Build/Solve progress in a longer-lived progress UI,
// since they close the channel when they're done.
func mirrorCh[T any](dest chan<- T) (chan T, *sync.WaitGroup) {
	wg := new(sync.WaitGroup)

	if dest == nil {
		return nil, wg
	}

	mirrorCh := make(chan T)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range mirrorCh {
			dest <- event
		}
	}()

	return mirrorCh, wg
}
