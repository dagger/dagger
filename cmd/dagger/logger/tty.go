package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/containerd/console"
	"github.com/morikuni/aec"
	"github.com/tonistiigi/vt100"
	"go.dagger.io/dagger/plan/task"
	"golang.org/x/sync/errgroup"
)

type Event map[string]interface{}

type Group struct {
	Name         string
	CurrentState task.State
	FinalState   task.State
	Events       []Event
	Started      time.Time
	Completed    time.Time
	Members      int
}

type Message struct {
	Event Event
	Group *Group
}

type Logs struct {
	Messages []Message

	groups map[string]*Group
	l      sync.Mutex
}

func (l *Logs) Add(event Event) error {
	l.l.Lock()
	defer l.l.Unlock()

	// same thing as in plain.go group all the non-identified tasks
	// into a general group called system
	source := systemGroup
	taskPath, ok := event["task"].(string)

	if ok && len(taskPath) > 0 {
		source = taskPath
	} else if !ok {
		l.Messages = append(l.Messages, Message{
			Event: event,
		})

		return nil
	}

	// Hide hidden fields (e.g. `._*`) from log group names
	groupKey := strings.Split(source, "._")[0]

	group := l.groups[groupKey]

	// If the group doesn't exist, create it
	if group == nil {
		group = &Group{
			Name:    groupKey,
			Started: time.Now(), // the: use UTC?
		}
		l.groups[groupKey] = group
		l.Messages = append(l.Messages, Message{
			Group: group,
		})
	}

	// Handle state events
	// For state events, we just want to update the group status -- no need to
	// display anything
	if st, ok := event["state"].(string); ok {
		t, err := task.ParseState(st)
		if err != nil {
			return err
		}

		if group.FinalState.CanTransition(t) {
			group.FinalState = t
		}

		if t == task.StateComputing {
			group.CurrentState = t
			group.Members++
			group.Completed = time.Time{}
		} else {
			group.Members--
			if group.Members <= 0 {
				group.Completed = time.Now()
				group.CurrentState = group.FinalState
			}
		}

		return nil
	}

	group.Events = append(group.Events, event)

	return nil
}

type TTYOutput struct {
	cons      ConsoleWriter
	logs      *Logs
	lineCount int
	l         sync.RWMutex

	stopCh  chan struct{}
	doneCh  chan struct{}
	printCh chan struct{}
}

type File interface {
	io.ReadWriteCloser

	// Fd returns its file descriptor
	Fd() uintptr
	// Name returns its file name
	Name() string
}

type ConsoleWriter interface {
	io.Writer
	ConsoleSizer
}

type ConsoleSizer interface {
	Size() (WinSize, error)
}

type ConsoleAdapter struct {
	Cons console.Console
	F    File
}

type WinSize console.WinSize

func (ca ConsoleAdapter) Size() (WinSize, error) {
	if ca.Cons == nil {
		return WinSize{}, errors.New("console adapter: console not set")
	}
	ws, err := ca.Cons.Size()
	if err != nil {
		return WinSize{}, err
	}
	s := WinSize(ws)
	return s, nil
}

func (ca *ConsoleAdapter) Write(b []byte) (int, error) {
	var b1, b2 []byte

	b1 = append(b1, b...)
	b2 = append(b2, b...)

	var g errgroup.Group

	g.Go(func() error {
		_, err := ca.Cons.Write(b1)
		if err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		_, err := ca.F.Write(b2)
		if err != nil {
			return err
		}
		return nil
	})

	err := g.Wait()
	if err != nil {
		return len(b), err
	}

	return len(b), nil
}

func NewTTYOutputConsole(w ConsoleWriter) (*TTYOutput, error) {
	c := &TTYOutput{
		logs: &Logs{
			groups: make(map[string]*Group),
		},
		cons:    w,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		printCh: make(chan struct{}, 128),
	}

	return c, nil
}

func NewTTYOutput(w File) (*TTYOutput, error) {
	cons, err := console.ConsoleFromFile(w)
	if err != nil {
		return nil, err
	}

	ca := &ConsoleAdapter{Cons: cons}

	c := &TTYOutput{
		logs: &Logs{
			groups: make(map[string]*Group),
		},
		cons:    ca,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		printCh: make(chan struct{}, 128),
	}

	return c, nil
}

func (c *TTYOutput) Start() {
	defer close(c.doneCh)
	go func() {
		for {
			select {
			case <-c.stopCh:
				return
			case <-c.printCh:
				c.print()
			case <-time.After(100 * time.Millisecond):
				c.print()
			}
		}
	}()
}

func (c *TTYOutput) Stop() {
	c.l.Lock()
	defer c.l.Unlock()

	if c.doneCh == nil {
		return
	}
	close(c.stopCh)
	<-c.doneCh
	c.doneCh = nil
}

func (c *TTYOutput) Write(p []byte) (n int, err error) {
	event := Event{}
	d := json.NewDecoder(bytes.NewReader(p))
	// FIXME decode in a loop in case the json data is a stream and not a document
	// https://mottaquikarim.github.io/dev/posts/you-might-not-be-using-json.decoder-correctly-in-golang/
	if err := d.Decode(&event); err != nil {
		return n, fmt.Errorf("cannot decode event: %s", err)
	}

	if err := c.logs.Add(event); err != nil {
		return 0, err
	}

	c.print()

	return len(p), nil
}

func (c *TTYOutput) print() {
	c.l.Lock()
	defer c.l.Unlock()

	// make sure the printer is not stopped
	select {
	case <-c.stopCh:
		return
	default:
	}

	width, height := getSize(c.cons)
	print(&c.lineCount, width, height, c.cons, c.logs.Messages)
}

func goBack(b *aec.Builder, lineCount int) *aec.Builder {
	if lineCount < 1 {
		lineCount = 0
	}
	b = b.Up(uint(lineCount))
	return b
}

func goBackLoop(b *aec.Builder, lineCount int) *aec.Builder {
	for i := 0; i < lineCount; i++ {
		b = b.Up(1)
	}
	return b
}

func print(lineCount *int, width, height int, cons io.Writer, messages []Message) {
	// hide during re-rendering to avoid flickering
	fmt.Fprint(cons, aec.Hide)
	defer fmt.Fprint(cons, aec.Show)

	// rewind to the top
	b := aec.EmptyBuilder

	b = goBack(b, *lineCount)

	fmt.Fprint(cons, b.ANSI)

	linesPerGroup := linesPerGroup(width, height, messages)
	lnCount := 0
	for _, message := range messages {
		if group := message.Group; group != nil {
			lnCount += printGroup(*group, width, linesPerGroup, cons)
		} else {
			lnCount += printLine(cons, message.Event, width)
		}
	}

	if diff := *lineCount - lnCount; diff > 0 {
		for i := 0; i < diff; i++ {
			fmt.Fprintln(cons, strings.Repeat(" ", width))
		}
		fmt.Fprint(cons, aec.EmptyBuilder.Up(uint(diff)).Column(0).ANSI)
	}

	*lineCount = lnCount
}

func linesPerGroup(width, height int, messages []Message) int {
	return linesPerGroupW(io.Discard, width, height, messages)
}

func linesPerGroupW(w io.Writer, width, height int, messages []Message) int {
	usedLines := 0
	for _, message := range messages {
		if group := message.Group; group != nil {
			usedLines++
			continue
		}
		usedLines += printLine(w, message.Event, width)
	}

	runningGroups := 0
	for _, message := range messages {
		if group := message.Group; group != nil && group.CurrentState == task.StateComputing {
			runningGroups++
		}
	}

	linesPerGroup := 5
	if freeLines := (height - usedLines); freeLines > 0 && runningGroups > 0 {
		linesPerGroup = (freeLines - 2) / runningGroups
	}

	return linesPerGroup
}

func printLine(w io.Writer, event Event, width int) int {
	message := colorize.Color(fmt.Sprintf("%s %s %s%s",
		formatTimestamp(event),
		formatLevel(event),
		formatMessage(event),
		formatFields(event),
	))

	// pad
	if delta := width - utf8.RuneCountInString(message); delta > 0 {
		message += strings.Repeat(" ", delta)
	}
	message += "\n"

	// print
	fmt.Fprint(w, message)

	t := vt100.NewVT100(100, width)
	t.Write([]byte(message))
	return t.UsedHeight()
}

func statePrefix(state task.State) string {
	var prefix string
	switch state {
	case task.StateComputing:
		prefix = "[+] "
	case task.StateSkipped:
		prefix = "[-] "
	case task.StateCanceled:
		prefix = "[✗] "
	case task.StateFailed:
		prefix = "[✗] "
	case task.StateCompleted:
		prefix = "[✔] "
	default:
		prefix = ""
	}
	return prefix
}

func groupTimer(started, completed time.Time) string {
	endTime := time.Now()
	if !completed.IsZero() {
		endTime = completed
	}

	dt := endTime.Sub(started).Seconds()
	if dt < 0.05 {
		dt = 0
	}

	timer := fmt.Sprintf("%3.1fs", dt)
	return timer
}

func makeLine(prefix string, text string, timer string, width int) string {
	prefixLen := utf8.RuneCountInString(prefix)
	textLen := utf8.RuneCountInString(text)
	timerLen := utf8.RuneCountInString(timer)
	padLen := width - (prefixLen + textLen + timerLen)
	padLenAbs := int(math.Abs(float64(padLen)))

	var out string
	switch {
	case padLen >= 0:
		text = trimMessage(text, width)
		padding := strings.Repeat(" ", padLen)
		out = fmt.Sprintf("%s%s%s%s\n", prefix, text, padding, timer)
	case padLen < 0 && padLenAbs < textLen:
		oldLen := textLen
		text = trimMessage(text, textLen-padLenAbs)
		newLen := utf8.RuneCountInString(text)
		padding := strings.Repeat(" ", padLen+(oldLen-newLen))
		out = fmt.Sprintf("%s%s%s%s\n", prefix, text, padding, timer)
	case padLen < 0 && padLenAbs > prefixLen+1 /* message reduced to "…" */ +timerLen:
		text = "…"
		timer = ""
		out = fmt.Sprintf("%s%s%s\n", prefix, text, timer)
	case padLen < 0 && padLenAbs > prefixLen+1 /* message reduced to "…" */ +0 /* no timer info*/ :
		// width too small, let's just display 1 char
		out = "…"
	default:

		panic("oops")
		text = trimMessage(text, width)
		out = fmt.Sprintf("%s%s%s\n", prefix, text, timer)
	}
	return out
}

func printGroup(group Group, width, maxLines int, cons io.Writer) int {
	lineCount := 0

	// treat the "system" group as a special case as we don't
	// want it to be displayed as an action in the output
	if group.Name != systemGroup {
		prefix := statePrefix(group.CurrentState)
		timer := groupTimer(group.Started, group.Completed)
		out := makeLine(prefix, group.Name, timer, width)

		// color
		switch group.CurrentState {
		case task.StateComputing:
			out = aec.Apply(out, aec.LightBlueF)
		case task.StateSkipped:
			out = aec.Apply(out, aec.LightCyanF)
		case task.StateCanceled:
			out = aec.Apply(out, aec.LightYellowF)
		case task.StateFailed:
			out = aec.Apply(out, aec.LightRedF)
		case task.StateCompleted:
			out = aec.Apply(out, aec.LightGreenF)
		}

		// Print
		fmt.Fprint(cons, out)
		lineCount++
	}

	printEvents := []Event{}
	switch group.CurrentState {
	case task.StateComputing:
		printEvents = group.Events
		// for computing tasks, show only last N
		if len(printEvents) > maxLines && maxLines >= 0 {
			printEvents = printEvents[len(printEvents)-maxLines:]
		}
	case task.StateSkipped:
		// for skipped tasks, don't show any logs
		printEvents = []Event{}
	case task.StateCanceled:
		// for completed tasks, don't show any logs
		printEvents = []Event{}
	case task.StateFailed:
		// for failed, show all logs
		printEvents = group.Events
	case task.StateCompleted:
		// for completed tasks, don't show any logs
		printEvents = []Event{}
	}

	for _, event := range printEvents {
		lineCount += printGroupLine(event, width, cons)
	}

	return lineCount
}

func printGroupLine(event Event, width int, cons io.Writer) (nbLines int) {
	message, nbLines := formatGroupLine(event, width)

	// Print
	fmt.Fprint(cons, message)

	return nbLines
}

func trimMessage(message string, width int) string {
	if width == 0 {
		return ""
	}
	s := message

	for sLen := utf8.RuneCountInString(s); sLen > width; sLen = utf8.RuneCountInString(s) {
		offset := 4
		if sLen < 4 {
			offset = sLen
		}
		s = s[0:sLen-offset] + "…"
	}
	return s
}

func formatGroupLine(event Event, width int) (message string, nbLines int) {
	message = colorize.Color(fmt.Sprintf("%s%s",
		formatMessage(event),
		formatFields(event),
	))

	message = trimMessage(message, width)

	// pad
	if delta := width - utf8.RuneCountInString(message); delta > 0 {
		message += strings.Repeat(" ", delta)
	}
	message += "\n"

	// color
	message = aec.Apply(message, aec.Faint)

	return message, 1
}

func getSize(cons ConsoleSizer) (width, height int) {
	width = 80
	height = 10
	if cons == nil {
		return width, height
	}

	size, err := cons.Size()
	if err == nil && size.Width > 0 && size.Height > 0 {
		width = int(size.Width)
		height = int(size.Height)
	}

	return width, height
}
