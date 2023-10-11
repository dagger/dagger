package core

import (
	"github.com/dagger/dagger/core/resourceid"
	"github.com/opencontainers/go-digest"
)

type GeneratedCode struct {
	Code              *Directory `json:"code"`
	VCSIgnoredPaths   []string   `json:"vcsIgnoredPaths,omitempty"`
	VCSGeneratedPaths []string   `json:"vcsGeneratedPaths,omitempty"`
}

func (code *GeneratedCode) ID() (GeneratedCodeID, error) {
	return resourceid.Encode(code)
}

func (code *GeneratedCode) Digest() (digest.Digest, error) {
	return stableDigest(code)
}

func (code GeneratedCode) Clone() *GeneratedCode {
	cp := code
	if cp.Code != nil {
		cp.Code = cp.Code.Clone()
	}
	return &cp
}

func (code *GeneratedCode) WithCode(dir *Directory) *GeneratedCode {
	code = code.Clone()
	code.Code = dir
	return code
}

func (code *GeneratedCode) WithVCSIgnoredPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSIgnoredPaths = paths
	return code
}

func (code *GeneratedCode) WithVCSGeneratedPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSGeneratedPaths = paths
	return code
}
