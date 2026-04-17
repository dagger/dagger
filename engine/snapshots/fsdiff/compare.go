package fsdiff

type Comparison uint8

const (
	CompareCompat Comparison = iota
	CompareContentOnMetadataMatch
)
