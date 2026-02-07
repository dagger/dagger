//go:build darwin || windows

package core

func bindMountDir(source, target string) error {
	panic("bindMountDir is implemented only on linux")
}

func unmountDir(target string) error {
	panic("unmountDir is implemented only on linux")
}
