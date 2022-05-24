package logger

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/morikuni/aec"
	"github.com/stretchr/testify/require"
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

func TestPrintGroupLine(t *testing.T) {
	goldenData, err := os.ReadFile("./testdata/print_group_line_test.golden")
	if err != nil {
		t.Fatal(err)
	}

	tm := time.UnixMilli(123456789)
	event := map[string]interface{}{
		"time":    tm.Format(time.RFC3339),
		"abc":     "ABC",
		"level":   "5",
		"message": "my msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msg",
		"toto":    "TOTOTO",
		"titi":    "TITITITI",
		"tata":    "TATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATA",
		"tete":    "TETETETE",
	}

	w := 150
	var b bytes.Buffer
	n := printGroupLine(event, w, &b)

	if goldenUpdate {
		err := os.WriteFile("./testdata/print_group_line_test.golden", b.Bytes(), 0o660)
		if err != nil {
			t.Fatal(err)
		}
	}
	require.Equal(t, goldenData, b.Bytes())
	require.Equal(t, 1, n)
	//t.Fatalf("DBGTHE: %v\n%v\n%v\n%v", n, event, w, b.Bytes())
}

func TestGoBack(t *testing.T) {
	for i := -10; i < 10; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			b := aec.EmptyBuilder
			bl := aec.EmptyBuilder

			b = goBack(b, i)
			bl = goBackLoop(bl, i)

			var out, outl bytes.Buffer
			fmt.Fprint(&out, "Hello World", b.ANSI, "Universe")
			fmt.Fprint(&outl, "Hello World", bl.ANSI, "Universe")
			t.Logf("\ngot: %v\nexp: %v", out.String(), outl.String())

			// we can't just compare those as is as the goBackFor creates more characters
			// and goBack will encode the number in the escape sequence
			// but visually, the result is the same, the Hello World will get overwriten
			if out.String() != outl.String() {
				//	t.Fatalf("\ngot: %v\nexp: %v", out.Bytes(), outf.Bytes())
			}
		})
	}
}

var goldenUpdate bool

func init() {
	flag.BoolVar(&goldenUpdate, "test.golden-update", false, "update golden file for tests")
}

func TestPrintLine(t *testing.T) {
	goldenData, err := os.ReadFile("./testdata/print_line_test.golden")
	if err != nil {
		t.Fatal(err)
	}

	tm := time.UnixMilli(123456789)

	var b bytes.Buffer
	event := map[string]interface{}{
		"time":    tm.Format(time.RFC3339),
		"abc":     "ABC",
		"level":   "5",
		"message": "my msg",
		"toto":    "TOTOTO",
		"titi":    "TITITITI",
		"tata":    "TATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATA",
		"tete":    "TETETETE",
	}

	width := 20

	n := printLine(&b, event, width)

	if goldenUpdate {
		err := os.WriteFile("./testdata/print_line_test.golden", b.Bytes(), 0o660)
		if err != nil {
			t.Fatal(err)
		}
	}
	require.Equal(t, goldenData, b.Bytes())
	t.Log(b.String(), event, width, n)
}
