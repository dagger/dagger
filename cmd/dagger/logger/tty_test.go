package logger

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type mockConsole struct {
	buf  bytes.Buffer
	size WinSize
}

func (c mockConsole) Write(b []byte) (int, error) {
	return c.buf.Write(b)
}

func (c mockConsole) Size() (WinSize, error) {
	return c.size, nil
}

func TestTTYOutput(t *testing.T) {
	f := &MockFile{exp: []byte("lol")}
	n, err := f.Write([]byte("lol"))
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("can not write")
	}

	diff := cmp.Diff(f.exp, f.content)
	if diff != "" {
		t.Fatal("output not expected:\n", diff)
	}

	m := mockConsole{}
	ttyo, err := NewTTYOutputConsole(&m)
	if err != nil {
		t.Fatal(err)
	}

	ttyo.Start()
	defer ttyo.Stop()

	_ = ttyo
}

type MockFile struct {
	content []byte
	exp     []byte
}

func (f MockFile) Read(p []byte) (n int, err error) {
	sz := len(p)
	p = p[:0]
	if sz > len(f.content)-1 {
		sz = len(f.content) - 1
	}
	p = append(p, f.content[0:sz]...)
	_ = p
	return 0, nil
}

func (f *MockFile) Write(p []byte) (n int, err error) {
	f.content = append(f.content, p...)
	return len(p), nil
}

func (f MockFile) Close() error {
	return nil
}

func (f MockFile) Fd() uintptr {
	return 2
}

func (f MockFile) Name() string {
	return "MockFile"
}

func TestGetSize(t *testing.T) {
	defaults := struct {
		W int
		H int
	}{
		W: 80,
		H: 10,
	}

	t.Run("nil ConsoleWriter", func(t *testing.T) {
		w, h := getSize(nil)

		if w != defaults.W || h != defaults.H {
			t.Fatalf("expected %dx%d, got %dx%d", defaults.W, defaults.H, w, h)
		}
	})

	t.Run("nil console", func(t *testing.T) {
		w, h := getSize(&ConsoleAdapter{
			Cons: nil,
			F:    &MockFile{},
		})

		if w != defaults.W || h != defaults.H {
			t.Fatalf("expected %dx%d, got %dx%d", defaults.W, defaults.H, w, h)
		}
	})

	t.Run("console with error in Size()", func(t *testing.T) {
		sizerMock := sizerMock{
			sizeFunc: func() (WinSize, error) {
				return WinSize{}, errors.New("error in size")
			},
		}
		w, h := getSize(sizerMock)

		if w != defaults.W || h != defaults.H {
			t.Fatalf("expected %dx%d, got %dx%d", defaults.W, defaults.H, w, h)
		}
	})

	t.Run("console with 300x100", func(t *testing.T) {
		expW, expH := uint16(100), uint16(300)
		sizerMock := sizerMock{
			sizeFunc: func() (WinSize, error) {
				return WinSize{
					Width:  expW,
					Height: expH,
				}, nil
			},
		}
		w, h := getSize(sizerMock)

		if w != int(expW) || h != int(expH) {
			t.Fatalf("expected %dx%d, got %dx%d", expW, expH, w, h)
		}
	})
}

type sizerMock struct {
	sizeFunc func() (WinSize, error)
}

func (s sizerMock) Size() (WinSize, error) {
	return s.sizeFunc()
}
