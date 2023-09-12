package core

import (
	"github.com/dagger/dagger/core/idproto"
	"github.com/opencontainers/go-digest"
)

type GeneratedCode struct {
	ID *idproto.ID `json:"id,omitempty"`

	Code              *Directory `json:"code"`
	VCSIgnoredPaths   []string   `json:"vcsIgnoredPaths,omitempty"`
	VCSGeneratedPaths []string   `json:"vcsGeneratedPaths,omitempty"`
}

func NewGeneratedCode(code *Directory) *GeneratedCode {
	return &GeneratedCode{
		Code: code,
	}
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
