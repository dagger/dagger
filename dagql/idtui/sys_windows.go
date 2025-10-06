package idtui

import "os"

func sigquit() {
	// TODO?
}

func openInputTTY() (*os.File, error) {
	f, err := os.OpenFile("CONIN$", os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}
