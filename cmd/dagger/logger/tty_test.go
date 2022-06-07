package logger

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/morikuni/aec"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/vt100"
	"go.dagger.io/dagger/plan/task"
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
	tm := time.UnixMilli(123456789)
	event := map[string]interface{}{
		"time":    tm.Format(time.RFC3339),
		"abc":     "ABC",
		"level":   "5",
		"message": "my msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msgmy msg",
		"tutu":    "TUTUTUTU",
		"toto":    "TOTOTO",
		"titi":    "TITITITI",
		"tata":    "TATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATATA",
		"tete":    "TETETETE",
	}

	for _, w := range []int{20, 200, 1000} {
		t.Run(fmt.Sprintf("width=%d", w), func(t *testing.T) {
			goldenFilePath := fmt.Sprintf("./testdata/print_group_line_test_w%d.golden", w)
			width := w

			var b bytes.Buffer
			n := printGroupLine(event, width, &b)

			if goldenUpdate {
				err := os.WriteFile(goldenFilePath, b.Bytes(), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			goldenData, err := os.ReadFile(goldenFilePath)
			if err != nil {
				t.Fatal(err)
			}

			require.Equal(t, goldenData, b.Bytes())
			require.Equal(t, 1, n)
			// t.Fatalf("DBGTHE: %v\n%v\n%v\n%v", n, event, w, b.Bytes())
		})
	}
}

func TestPrint(t *testing.T) {
	b, err := ioutil.TempFile("/tmp", time.Now().Format("2006-01-02_15h04m05_")+"dagger-test-*.out")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("too small console width", func(t *testing.T) {
		now := time.Now().UTC()
		msgs := []Message{
			{
				Event: map[string]interface{}{"abc": "ABC"},
				Group: &Group{
					Name:    "test",
					Started: now,
				},
			},
		}
		lc := 1

		// fails with w=11 and group len(name)=4
		// we need:
		// - 4 to display "[+] "
		// - 4 to display "test"
		// - 4 chars to display "0.0s"
		print(&lc, 11, 1, b, msgs)
	})
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
			// if out.String() != outl.String() {
			// 	t.Fatalf("\ngot: %v\nexp: %v", out.Bytes(), outf.Bytes())
			// }
		})
	}
}

var goldenUpdate bool

func init() {
	flag.BoolVar(&goldenUpdate, "test.golden-update", false, "update golden file for tests")
}

func TestPrintLine(t *testing.T) {
	tm := time.UnixMilli(123456789).UTC()

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

	for _, w := range []int{20, 200} {
		t.Run(fmt.Sprintf("width=%d", w), func(t *testing.T) {
			goldenFilePath := fmt.Sprintf("./testdata/print_line_test_w%d.golden", w)
			width := w

			var b bytes.Buffer
			n := printLine(&b, event, width)

			if goldenUpdate {
				err := os.WriteFile(goldenFilePath, b.Bytes(), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}
			goldenData, err := os.ReadFile(goldenFilePath)
			if err != nil {
				t.Fatal(err)
			}

			require.Equal(t, goldenData, b.Bytes())
			t.Log(b.String(), event, width, n)
		})
	}
}

func TestLinesPerGroup(t *testing.T) {
	w, h := 10, 25
	now := time.Now().UTC()
	msgs := []Message{
		{
			Event: map[string]interface{}{"abc": "ABC"},
			Group: &Group{
				Name:    "test1",
				Started: now,
			},
		},
		{
			Event: map[string]interface{}{"def": "DEF"},
			Group: &Group{
				Name:    "test1",
				Started: now,
			},
		},
		{
			Event: map[string]interface{}{"ghi": "GHI"},
			Group: &Group{
				Name:    "test2",
				Started: now,
			},
		},
	}

	var b bytes.Buffer
	n := linesPerGroupW(&b, w, h, msgs)
	_ = n
	// TODO: add test check
	// t.Error(n, b.String())
}

func TestPrintGroup(t *testing.T) {
	t.Run("too small terminal", func(t *testing.T) {
		g := Group{
			Name:    "grp1",
			Started: time.Now(),
			Events: []Event{
				{
					"abc": "ABC",
				},
				{
					"def": "DEF",
				},
			},
		}

		w := 9 // TODO: with 8, it panics
		maxL := 1
		var b bytes.Buffer

		n := printGroup(g, w, maxL, &b)

		// we use a big enough vt to make sure
		// our algo actually wraps correctly
		vt := vt100.NewVT100(100, 1000)
		nn, err := vt.Write(b.Bytes())
		require.Equal(t, nn, b.Len())
		require.NoError(t, err)

		// we test with the 1st line of the output
		ln1 := string(vt.Content[0])
		trimmed := strings.TrimSpace(ln1)
		trimmedLen := utf8.RuneCountInString(trimmed)

		require.LessOrEqual(t, trimmedLen, w, "\ngot: %q\nexp: %q\n\nn=%d\nb=%q\n", trimmed, ln1, n, b.String())
	})
}

func TestTrimMessage(t *testing.T) {
	cases := []struct {
		width int
		msg   string
		exp   string
	}{
		{0, "testing12", ""},
		{1, "testing12", "…"},
		{5, "testing12", "te…"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("width=%d", c.width), func(t *testing.T) {
			got := trimMessage(c.msg, c.width)

			require.Equal(t, c.exp, got)
		})
	}
}

func termLineLen(t *testing.T, text string) int {
	t.Helper()
	vt := vt100.NewVT100(100, 1000)
	_, err := vt.Write([]byte(text))
	require.NoError(t, err)

	// we test with the 1st line of the output
	ln1 := string(vt.Content[0])
	trimmed := strings.TrimSpace(ln1)
	trimmedLen := utf8.RuneCountInString(trimmed)

	return trimmedLen
}

func compTermLineLen(t *testing.T, exp, got string) {
	t.Helper()
	expLen := termLineLen(t, exp)
	gotLen := termLineLen(t, got)
	require.Equal(t, expLen, gotLen, "\nexp=%s\ngot=%s\n", exp, got)
}

func TestColorLine(t *testing.T) {
	cases := []task.State{
		task.StateComputing,
		task.StateSkipped,
		task.StateCompleted,
		task.StateCanceled,
		task.StateFailed,
	}

	text := "This is just a test"
	for _, c := range cases {
		t.Run(c.String(), func(t *testing.T) {
			got := colorLine(c, text)
			compTermLineLen(t, text, got)
		})
	}
}
