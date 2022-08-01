package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/morikuni/aec"
	"github.com/stretchr/testify/assert"
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
	f := &mockFile{exp: []byte("lol")}
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

type mockFile struct {
	content []byte
	exp     []byte
}

func (f mockFile) Read(p []byte) (n int, err error) {
	sz := len(p)
	p = p[:0]
	if sz > len(f.content)-1 {
		sz = len(f.content) - 1
	}
	p = append(p, f.content[0:sz]...)
	_ = p
	return 0, nil
}

func (f *mockFile) Write(p []byte) (n int, err error) {
	f.content = append(f.content, p...)
	return len(p), nil
}

func (f mockFile) Close() error {
	return nil
}

func (f mockFile) Fd() uintptr {
	return 2
}

func (f mockFile) Name() string {
	return "MockFileV2"
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
			F:    &mockFile{},
		})

		if w != defaults.W || h != defaults.H {
			t.Fatalf("expected %dx%d, got %dx%d", defaults.W, defaults.H, w, h)
		}
	})

	t.Run("console with error in Size()", func(t *testing.T) {
		sizerMockV2 := sizerMockV2{
			sizeFunc: func() (WinSize, error) {
				return WinSize{}, errors.New("error in size")
			},
		}
		w, h := getSize(sizerMockV2)

		if w != defaults.W || h != defaults.H {
			t.Fatalf("expected %dx%d, got %dx%d", defaults.W, defaults.H, w, h)
		}
	})

	t.Run("console with 300x100", func(t *testing.T) {
		expW, expH := uint16(100), uint16(300)
		sizerMockV2 := sizerMockV2{
			sizeFunc: func() (WinSize, error) {
				return WinSize{
					Width:  expW,
					Height: expH,
				}, nil
			},
		}
		w, h := getSize(sizerMockV2)

		if w != int(expW) || h != int(expH) {
			t.Fatalf("expected %dx%d, got %dx%d", expW, expH, w, h)
		}
	})
}

type sizerMockV2 struct {
	sizeFunc func() (WinSize, error)
}

func (s sizerMockV2) Size() (WinSize, error) {
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
		msgs := []MessageV2{
			{
				EventV2: map[string]interface{}{"abc": "ABC"},
				GroupV2: &GroupV2{
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
			n := printEvent(&b, event, width)

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
	width, height := 10, 25
	now := time.Now().UTC()
	msgs := []MessageV2{
		{
			EventV2: map[string]interface{}{"abc": "ABC"},
			GroupV2: &GroupV2{
				Name:    "test1",
				Started: now,
			},
		},
		{
			EventV2: map[string]interface{}{"def": "DEF"},
			GroupV2: &GroupV2{
				Name:    "test1",
				Started: now,
			},
		},
		{
			EventV2: map[string]interface{}{"ghi": "GHI"},
			GroupV2: &GroupV2{
				Name:    "test2",
				Started: now,
			},
		},
		{
			EventV2: map[string]interface{}{"klm": "KLM"},
		},
	}

	n := linesPerGroup(width, height, msgs)

	require.Equal(t, 5, n)
}

func TestPrintGroup(t *testing.T) {
	t.Run("too small terminal", func(t *testing.T) {
		g := GroupV2{
			Name:    "grp1",
			Started: time.Now(),
			Events: []EventV2{
				{
					"abc": "ABC",
				},
				{
					"def": "DEF",
				},
			},
		}

		w := 8
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
		{2, "00", "00"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("width=%d", c.width), func(t *testing.T) {
			got := trimMessage(c.msg, c.width)

			lGot := utf8.RuneCountInString(got)
			lMsg := utf8.RuneCountInString(c.msg)

			require.LessOrEqual(t, lGot, lMsg, "\nmsg: %v\ngot: %v\n", c.msg, got)
			require.Equal(t, c.exp, got)
		})
	}
}

func FuzzTrimMessage(f *testing.F) {
	f.Add("testing12", 0)
	f.Add("actions.all.test", 12)

	f.Fuzz(func(t *testing.T, msg string, width int) {
		got := trimMessage(msg, width)

		lGot := utf8.RuneCountInString(got)
		lMsg := utf8.RuneCountInString(msg)

		require.LessOrEqual(t, lGot, lMsg, "\nmsg: %v\ngot: %v\n", msg, got)
	})
}

func escapeLine(t *testing.T, text string) (string, int) {
	t.Helper()
	vt := vt100.NewVT100(100, 1000)
	_, err := vt.Write([]byte(text))
	require.NoError(t, err)

	// we test with the 1st line of the output
	ln1 := string(vt.Content[0])
	trimmed := strings.TrimSpace(ln1)
	trimmedLen := utf8.RuneCountInString(trimmed)

	return trimmed, trimmedLen
}

// compareTerminalLineLength compare term line length once it has been
// interpreted (escape codes, etc)
func compareTerminalLineLength(t *testing.T, exp, got string) {
	t.Helper()
	escExp, expLen := escapeLine(t, exp)
	escGot, gotLen := escapeLine(t, got)
	require.Equal(t, expLen, gotLen, "\nexp=%s\ngot=%s\n", exp, got)
	require.Equal(t, escExp, escGot, "\nexp=%s\ngot=%s\n", exp, got)
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
			compareTerminalLineLength(t, text, got)
		})
	}
}

func TestMakeLine(t *testing.T) {
	cases := map[string]struct {
		width int

		prefix string
		text   string
		timer  string

		exp string
	}{
		"30":           {30, "[+] ", "test mesg, test message, test message", "1.9s", "[+] test mesg, test messa 1.9s"},
		"12+big timer": {12, "[+] ", "test", "1234.9s", "[+]  1234.9s"},
		"12":           {12, "[+] ", "test", "1.9s", "[+] test1.9s"},
		"11":           {11, "[+] ", "test", "1.9s", "[+]    1.9s"},
		"9":            {9, "[+] ", "test", "1.9s", "[+]  1.9s"},
		"8":            {8, "[+] ", "test", "1.9s", "…"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := makeLine(c.prefix, c.text, c.timer, c.width)

			compareTerminalLineLength(t, c.exp, got)
		})
	}
}

func TestGetGroup(t *testing.T) {
	cases := map[string]struct {
		event EventV2

		ok  bool
		exp string
	}{
		"empty event":       {EventV2{}, false, systemGroup},
		"task not a string": {EventV2{"task": 1}, false, systemGroup},
		"task is a string":  {EventV2{"task": "group1"}, true, "group1"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			groupName, ok := getGroupName(c.event)
			require.Equal(t, c.ok, ok)
			require.Equal(t, c.exp, groupName)
		})
	}
}

func FuzzLogsAdd(f *testing.F) {
	seedEvents := []EventV2{
		{"task": "group1"},
		{"task": "group1", "state": "completed"},
		{"task": "actions.all.hellobis._exec", "state": "computing"},
		{"task": "actions.all.hello._exec", "state": "skipped"},
		{"task": "actions.all._hellobis._exec", "state": "cancelled"},
		{"task": "actions.all.hellobis._exec", "state": "failed"},
		{"state": "started"},
		{"task": "actions.all._hellobis._exec", "state": "canceled"}, // bad spelled cancelled
	}
	for _, e := range seedEvents {
		b, _ := json.Marshal(e)
		f.Add(b)
	}
	lAdd := LogsV2{
		groups: make(map[string]*GroupV2),
	}
	lSplitAdd := LogsV2{
		groups: make(map[string]*GroupV2),
	}
	f.Fuzz(func(t *testing.T, eventBytes []byte) {
		event := EventV2{}
		err := json.Unmarshal(eventBytes, &event)
		if err != nil {
			return
		}
		if len(event) == 0 {
			return
		}
		_, ok := event[""]
		if ok {
			return
		}
		for _, v := range event {
			if v == "" {
				return
			}
		}

		t.Run(fmt.Sprintf("%q", eventBytes), func(t *testing.T) {
			errAdd := lAdd.oldAdd(event)
			errSplitAdd := lSplitAdd.Add(event)

			require.Equal(t, errAdd, errSplitAdd)
			if errAdd != nil {
				// no need to test further
				return
			}
			require.Equal(t, len(lAdd.groups), len(lSplitAdd.groups))

			var lAddGroupsName sort.StringSlice
			for n := range lAdd.groups {
				lAddGroupsName = append(lAddGroupsName, n)
			}
			var lSplitAddGroupsName sort.StringSlice
			for n := range lAdd.groups {
				lSplitAddGroupsName = append(lSplitAddGroupsName, n)
			}

			lAddGroupsName.Sort()
			lSplitAddGroupsName.Sort()
			require.Equal(t, lAddGroupsName, lSplitAddGroupsName)

			for i := range lAdd.Messages {
				a := lAdd.Messages[i]
				b := lSplitAdd.Messages[i]

				require.Equal(t, a.EventV2, b.EventV2)
				if a.GroupV2 == nil && b.GroupV2 == nil {
					return
				}
				require.NotNil(t, a.GroupV2.Name)
				require.NotNil(t, b.GroupV2.Name)
				require.Equal(t, a.GroupV2.Name, b.GroupV2.Name, "\n%+v\n%+v\n", a.GroupV2, b.GroupV2)
				require.Equal(t, a.GroupV2.Members, b.GroupV2.Members)
				require.Equal(t, a.GroupV2.CurrentState, b.GroupV2.CurrentState)
				require.Equal(t, a.GroupV2.FinalState, b.GroupV2.FinalState)
				require.Equal(t, a.GroupV2.Events, b.GroupV2.Events)
			}
		})
	})
}

func TestFormatGroupLine(t *testing.T) {
	cases := map[string]struct {
		event EventV2
		width int
		exp   string
	}{
		"ok":  {EventV2{"message": "simple message", "level": "error", "error": "does not work", "field1": "value1"}, 20, "\x1b[2m\x1b[31msimple message: d…  \n\x1b[0m"}, // FIXME: there shouldn't be 2 space after the …
		"ok2": {EventV2{"message": "simple message", "level": "error", "error": "does not work", "field1": "value1"}, 200, "\x1b[2m\x1b[31msimple message: does not work\x1b[0m    \x1b[1mfield1=value1\x1b[0m\x1b[0m                                                                                                                                                          \n\x1b[0m"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, n := formatGroupLine(c.event, c.width)
			require.LessOrEqual(t, printSize(got), c.width)
			require.Equal(t, n, 1)
			assert.Equal(t, c.exp, got, "%s", got)
		})
	}
}

func TestTermLen(t *testing.T) {
	n := termLen("  \x1b[2m\x1b[31mABC   some test    \x1b[0m   ", 3)
	require.Equal(t, 17, n)
}
