package snapshots

// copyDirMetadata leaves dst as created: Windows snapshots don't carry Unix
// directory metadata.
func copyDirMetadata(src, dst string) error {
	return nil
}
