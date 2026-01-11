//go:build !linux
// +build !linux

package copy

func (*copier) copyFile(source, target string) (didHardlink bool, err error) {
	return false, copyFile(source, target)
}
