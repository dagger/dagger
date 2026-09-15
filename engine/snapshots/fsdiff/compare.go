package fsdiff

type Comparison uint8

const (
	CompareCompat Comparison = iota
	CompareContentOnMetadataMatch
	// CompareInodeThenContent treats files backed by the same inode as
	// unchanged and compares content whenever distinct files have otherwise
	// matching metadata. Snapshots of a shared lineage resolve unchanged
	// files to the same backing inode even across separate mounts, so this
	// stays O(changed files) there while still catching files whose stat
	// happens to collide (same size and mtime, different bytes).
	CompareInodeThenContent
)
