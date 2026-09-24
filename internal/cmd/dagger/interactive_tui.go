package daggercmd

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
	"github.com/vito/midterm"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/session/terminal"
)

// currentDaggerClient exposes the engine session's API client to terminal
// sessions, which are initiated by the engine (not a command) and so have no
// client in scope.
var currentDaggerClient atomic.Pointer[dagger.Client]

// interactiveTUISession runs the -i exec-error UI: a file explorer for the
// failed container's filesystem on the left, and the debug terminal on the
// right. It implements idtui.ExecCommand so the progress TUI can hand it the
// real TTY via Frontend.Background.
type interactiveTUISession struct {
	session *terminal.SessionHandle
	dag     *dagger.Client

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

var _ idtui.ExecCommand = (*interactiveTUISession)(nil)

func newInteractiveTUISession(session *terminal.SessionHandle, dag *dagger.Client) *interactiveTUISession {
	return &interactiveTUISession{session: session, dag: dag}
}

func (ts *interactiveTUISession) SetStdin(r io.Reader)  { ts.stdin = r }
func (ts *interactiveTUISession) SetStdout(w io.Writer) { ts.stdout = w }
func (ts *interactiveTUISession) SetStderr(w io.Writer) { ts.stderr = w }

func (ts *interactiveTUISession) Run() error {
	model := newExplorerModel(ts.session, ts.dag)
	prog := tea.NewProgram(model,
		tea.WithInput(ts.stdin),
		tea.WithOutput(ts.stdout),
		tea.WithAltScreen(),
	)
	model.prog = prog

	_, err := prog.Run()
	model.close()
	return err
}

// ---------- messages --------------------------------------------------------

type termOutputMsg []byte

type sessionExitMsg struct{ err error }

type dirLoadedMsg struct {
	rel     string
	entries []string
	err     error
	gen     int
}

type fileLoadedMsg struct {
	rel      string
	contents string
	err      error
}

// exportDoneMsg reports the result of exporting an explorer entry to the host.
type exportDoneMsg struct {
	rel  string
	dest string
	err  error
}

// exportPrompt is the inline "export to" path editor shown in the status line.
type exportPrompt struct {
	rel   string // entry to export, relative to the workspace root
	isDir bool
	value []rune // host destination path being edited
}

// snapshotReadyMsg carries a fresh Workspace captured from the running
// terminal's live filesystem.
type snapshotReadyMsg struct {
	ws  *dagger.Workspace
	err error
	at  time.Time
}

// ---------- model -----------------------------------------------------------

type explorerFocus int

const (
	focusTerm explorerFocus = iota
	focusTree
)

const (
	explorerTreeWidth  = 32
	explorerMaxPreview = 128 << 10

	// Below this size a split view is unusable: the explorer is not opened at
	// all (the classic raw shell is used), and if the window shrinks below it
	// while open, the tree is hidden and the terminal takes the full width.
	explorerMinCols = 60
	explorerMinRows = 10
)

// explorerFits reports whether a terminal of the given size can host the
// explorer's split view.
func explorerFits(cols, rows int) bool {
	return cols >= explorerMinCols && rows >= explorerMinRows
}

type treeNode struct {
	name     string
	rel      string // path relative to the workspace root
	isDir    bool
	expanded bool
	loaded   bool
	loading  bool
	children []*treeNode
	depth    int
}

type explorerModel struct {
	session *terminal.SessionHandle
	dag     *dagger.Client
	prog    *tea.Program

	ctx    context.Context
	cancel context.CancelFunc

	ws      *dagger.Workspace
	workdir string

	// terminal pane
	vt        *midterm.Terminal
	stdinW    *io.PipeWriter
	stdinQ    chan []byte // ordered, non-blocking path into the session's stdin
	pending   [][]byte    // output received before the pane was laid out
	termCols  int
	termRows  int
	startOnce bool

	// tree pane
	root    *treeNode
	cursor  int
	scroll  int
	focus   explorerFocus
	gen     int // refresh generation; stale dir loads are dropped
	loadErr string

	refreshing bool

	// export (o)
	prompt    *exportPrompt
	exporting bool

	// preview overlay (replaces the tree pane)
	previewPath string
	previewText []string
	previewOff  int

	width  int
	height int
	status string

	exited  bool
	exitErr error
}

func newExplorerModel(session *terminal.SessionHandle, dag *dagger.Client) *explorerModel {
	ctx, cancel := context.WithCancel(context.Background())

	info := session.Info
	ctr := dagger.Ref[*dagger.Container](dag, dagger.ID(info.ContainerId))
	// Wrap the container's working directory in a Workspace: a uniform,
	// browsable (and later editable) view for the explorer.
	ws := ctr.Directory(info.Workdir).AsWorkspace()

	return &explorerModel{
		session: session,
		dag:     dag,
		ctx:     ctx,
		cancel:  cancel,
		ws:      ws,
		workdir: info.Workdir,
		root:    &treeNode{name: info.Workdir, rel: ".", isDir: true, expanded: true, depth: 0},
		focus:   focusTerm,
		status:  "exec failed — inspecting " + info.Workdir,
	}
}

func (m *explorerModel) close() {
	if m.stdinQ != nil {
		close(m.stdinQ)
		m.stdinQ = nil
	}
	if m.stdinW != nil {
		m.stdinW.Close()
	}
	m.cancel()
}

func (m *explorerModel) Init() tea.Cmd {
	return tea.Batch(
		m.startSession(),
		m.loadDir(m.root),
	)
}

// startSession wires the terminal gRPC session to the embedded vt pane.
func (m *explorerModel) startSession() tea.Cmd {
	if m.startOnce {
		return nil
	}
	m.startOnce = true

	stdinR, stdinW := io.Pipe()
	m.stdinW = stdinW
	// Keystrokes and terminal query responses share one ordered queue, drained
	// off the UI goroutine so a slow session can never stall rendering.
	m.stdinQ = make(chan []byte, 256)
	go func() {
		for b := range m.stdinQ {
			if _, err := stdinW.Write(b); err != nil {
				return
			}
		}
	}()

	out := &programWriter{prog: func() *tea.Program { return m.prog }}

	return func() tea.Msg {
		err := m.session.Run(stdinR, out, out)
		return sessionExitMsg{err: err}
	}
}

// programWriter forwards terminal output into the bubbletea loop.
type programWriter struct {
	prog func() *tea.Program
}

func (w *programWriter) Write(p []byte) (int, error) {
	if prog := w.prog(); prog != nil {
		buf := make([]byte, len(p))
		copy(buf, p)
		prog.Send(termOutputMsg(buf))
	}
	return len(p), nil
}

// ---------- data loading ----------------------------------------------------

func (m *explorerModel) loadDir(node *treeNode) tea.Cmd {
	if node.loading {
		return nil
	}
	node.loading = true
	ws, rel, gen, ctx := m.ws, node.rel, m.gen, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		entries, err := ws.Directory(rel).Entries(ctx)
		return dirLoadedMsg{rel: rel, entries: entries, err: err, gen: gen}
	}
}

func (m *explorerModel) loadFile(rel string) tea.Cmd {
	ws, ctx := m.ws, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		contents, err := ws.File(rel).Contents(ctx)
		if err == nil && len(contents) > explorerMaxPreview {
			contents = contents[:explorerMaxPreview] + "\n… (truncated)"
		}
		return fileLoadedMsg{rel: rel, contents: contents, err: err}
	}
}

func (m *explorerModel) findNode(rel string) *treeNode {
	var walk func(n *treeNode) *treeNode
	walk = func(n *treeNode) *treeNode {
		if n.rel == rel {
			return n
		}
		for _, c := range n.children {
			if found := walk(c); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(m.root)
}

// visibleNodes flattens the expanded tree for rendering and cursor math.
func (m *explorerModel) visibleNodes() []*treeNode {
	var nodes []*treeNode
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		nodes = append(nodes, n)
		if n.isDir && n.expanded {
			for _, c := range n.children {
				walk(c)
			}
		}
	}
	walk(m.root)
	return nodes
}

const terminalSnapshotQuery = `query TerminalSnapshot($terminalID: String!) {
  _interactiveTerminalSnapshot(terminalID: $terminalID) { id }
}`

// refresh snapshots the running terminal's live filesystem into a new
// Workspace. The result arrives as a snapshotReadyMsg, which swaps the
// workspace and reloads the tree.
func (m *explorerModel) refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	terminalID := m.session.Info.TerminalId
	if terminalID == "" {
		m.status = "refresh unsupported by this engine version"
		return nil
	}
	m.refreshing = true
	m.status = "refreshing…"
	dag, ctx := m.dag, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		var res struct {
			Snapshot struct {
				ID string
			} `json:"_interactiveTerminalSnapshot"`
		}
		err := dag.Do(ctx, &dagger.Request{
			Query:     terminalSnapshotQuery,
			OpName:    "TerminalSnapshot",
			Variables: map[string]any{"terminalID": terminalID},
		}, &dagger.Response{Data: &res})
		if err != nil {
			return snapshotReadyMsg{err: err}
		}
		dir := dagger.Ref[*dagger.Directory](dag, dagger.ID(res.Snapshot.ID))
		return snapshotReadyMsg{ws: dir.AsWorkspace(), at: time.Now()}
	}
}

// reloadTree re-lists every expanded directory against the current
// workspace, preserving expansion state (and the open preview).
func (m *explorerModel) reloadTree() tea.Cmd {
	m.gen++
	var cmds []tea.Cmd
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		if n.isDir && n.expanded {
			n.loaded = false
			n.loading = false
			cmds = append(cmds, m.loadDir(n))
			for _, c := range n.children {
				walk(c)
			}
		}
	}
	walk(m.root)
	if m.previewPath != "" {
		cmds = append(cmds, m.loadFile(m.previewPath))
	}
	return tea.Batch(cmds...)
}

// ---------- update ----------------------------------------------------------

func (m *explorerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.treeWidth() == 0 {
			m.focus = focusTerm
		}
		m.layoutTerminal()
		return m, nil

	case termOutputMsg:
		if m.vt == nil {
			m.pending = append(m.pending, msg)
			return m, nil
		}
		_, _ = m.vt.Write(msg)
		return m, nil

	case sessionExitMsg:
		m.exited = true
		m.exitErr = msg.err
		return m, tea.Quit

	case dirLoadedMsg:
		if msg.gen != m.gen {
			return m, nil // stale: a refresh superseded this load
		}
		node := m.findNode(msg.rel)
		if node == nil {
			return m, nil
		}
		node.loading = false
		node.loaded = true
		if msg.err != nil {
			m.loadErr = fmt.Sprintf("list %s: %v", msg.rel, msg.err)
			return m, nil
		}
		node.children = buildChildren(node, msg.entries)
		return m, nil

	case exportDoneMsg:
		m.exporting = false
		if msg.err != nil {
			m.loadErr = fmt.Sprintf("export %s: %v", msg.rel, msg.err)
			m.status = "export failed"
			return m, nil
		}
		m.loadErr = ""
		m.status = "exported " + msg.rel + " → " + msg.dest
		return m, nil

	case snapshotReadyMsg:
		m.refreshing = false
		if msg.err != nil {
			m.loadErr = fmt.Sprintf("refresh: %v", msg.err)
			m.status = "refresh failed"
			return m, nil
		}
		m.loadErr = ""
		m.ws = msg.ws
		m.status = "refreshed at " + msg.at.Format("15:04:05")
		return m, m.reloadTree()

	case fileLoadedMsg:
		if msg.err != nil {
			if msg.rel == m.previewPath {
				// the previewed file was removed in the terminal
				m.previewText = []string{"(file no longer exists)"}
				m.previewOff = 0
				return m, nil
			}
			m.loadErr = fmt.Sprintf("read %s: %v", msg.rel, msg.err)
			return m, nil
		}
		sameFile := msg.rel == m.previewPath
		m.previewPath = msg.rel
		m.previewText = strings.Split(sanitizePreview(msg.contents), "\n")
		if !sameFile {
			m.previewOff = 0
		} else if m.previewOff >= len(m.previewText) {
			m.previewOff = max(0, len(m.previewText)-1)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func buildChildren(parent *treeNode, entries []string) []*treeNode {
	children := make([]*treeNode, 0, len(entries))
	for _, entry := range entries {
		isDir := strings.HasSuffix(entry, "/")
		name := strings.TrimSuffix(entry, "/")
		if name == "" {
			continue
		}
		rel := name
		if parent.rel != "." {
			rel = path.Join(parent.rel, name)
		}
		children = append(children, &treeNode{
			name:  name,
			rel:   rel,
			isDir: isDir,
			depth: parent.depth + 1,
		})
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].isDir != children[j].isDir {
			return children[i].isDir
		}
		return children[i].name < children[j].name
	})
	return children
}

// sanitizePreview makes file contents safe to draw in the preview pane:
// binary files (a NUL byte in the first 8 KiB, git's heuristic) are not shown,
// and other control characters (e.g. ANSI escapes in logs) are replaced so
// they can't drive the host terminal.
func sanitizePreview(s string) string {
	if strings.IndexByte(s[:min(len(s), 8<<10)], 0) >= 0 {
		return "(binary file)"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || !unicode.IsPrint(r):
			b.WriteRune('·')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *explorerModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The export prompt, when open, takes all input.
	if m.prompt != nil {
		return m.handlePromptKey(msg)
	}
	// Global: refresh the explorer from the terminal's live filesystem. F5
	// works from either pane (ctrl+r stays with the shell's reverse-search).
	if msg.Type == tea.KeyF5 {
		return m, m.refresh()
	}
	// Global: switch pane focus.
	if msg.Type == tea.KeyCtrlQ {
		if m.treeWidth() == 0 {
			m.status = "window too small for the file explorer"
			return m, nil
		}
		if m.focus == focusTerm {
			m.focus = focusTree
		} else {
			m.focus = focusTerm
		}
		return m, nil
	}

	if m.focus == focusTerm {
		if b := keyToBytes(msg); len(b) > 0 {
			m.sendStdin(b)
		}
		return m, nil
	}

	if m.previewPath != "" {
		return m.handlePreviewKey(msg)
	}
	return m.handleTreeKey(msg)
}

// handlePreviewKey handles keys while the file preview replaces the tree.
func (m *explorerModel) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "o":
		m.openExportPrompt(m.previewPath, false)
	case "esc", "q":
		m.previewPath = ""
		m.previewText = nil
	case "up", "k":
		if m.previewOff > 0 {
			m.previewOff--
		}
	case "down", "j":
		if m.previewOff < len(m.previewText)-1 {
			m.previewOff++
		}
	case "pgup":
		m.previewOff = max(0, m.previewOff-m.paneRows())
	case "pgdown":
		m.previewOff = min(max(0, len(m.previewText)-1), m.previewOff+m.paneRows())
	}
	return m, nil
}

// handleTreeKey handles keys while the file tree has focus.
func (m *explorerModel) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	nodes := m.visibleNodes()
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(nodes)-1 {
			m.cursor++
		}
	case "pgup":
		m.cursor = max(0, m.cursor-m.paneRows())
	case "pgdown":
		m.cursor = min(len(nodes)-1, m.cursor+m.paneRows())
	case "left", "h":
		if m.cursor < len(nodes) {
			if n := nodes[m.cursor]; n.isDir && n.expanded {
				n.expanded = false
			}
		}
	case "enter", "right", "l", " ":
		if m.cursor < len(nodes) {
			n := nodes[m.cursor]
			if n.isDir {
				n.expanded = !n.expanded
				if n.expanded && !n.loaded {
					return m, m.loadDir(n)
				}
			} else {
				m.loadErr = ""
				return m, m.loadFile(n.rel)
			}
		}
	case "ctrl+r", "r":
		return m, m.refresh()
	case "o":
		if m.cursor < len(nodes) {
			n := nodes[m.cursor]
			m.openExportPrompt(n.rel, n.isDir)
		}
	}
	m.clampScroll()
	return m, nil
}

// openExportPrompt starts editing the host destination for exporting rel,
// defaulting to ./<name> relative to where dagger was run.
func (m *explorerModel) openExportPrompt(rel string, isDir bool) {
	if m.exporting {
		return
	}
	name := path.Base(rel)
	if rel == "." {
		name = path.Base(m.workdir)
		if name == "/" || name == "." {
			name = "rootfs"
		}
	}
	m.prompt = &exportPrompt{
		rel:   rel,
		isDir: isDir,
		value: []rune("./" + name),
	}
}

func (m *explorerModel) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.prompt
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.prompt = nil
		return m, nil
	case tea.KeyEnter:
		dest := strings.TrimSpace(string(p.value))
		m.prompt = nil
		if dest == "" {
			return m, nil
		}
		return m, m.export(p.rel, p.isDir, dest)
	case tea.KeyBackspace:
		if len(p.value) > 0 {
			p.value = p.value[:len(p.value)-1]
		}
	case tea.KeyCtrlU:
		p.value = nil
	case tea.KeyCtrlW:
		// delete the last path segment
		s := strings.TrimRight(string(p.value), "/")
		if i := strings.LastIndex(s, "/"); i >= 0 {
			p.value = []rune(s[:i+1])
		} else {
			p.value = nil
		}
	case tea.KeySpace:
		p.value = append(p.value, ' ')
	case tea.KeyRunes:
		p.value = append(p.value, msg.Runes...)
	}
	return m, nil
}

// export writes the entry to the host via Directory.Export / File.Export,
// from the explorer's current snapshot (what you see is what you export).
func (m *explorerModel) export(rel string, isDir bool, dest string) tea.Cmd {
	m.exporting = true
	m.status = "exporting " + rel + "…"
	ws, ctx := m.ws, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		var (
			exported string
			err      error
		)
		if isDir {
			exported, err = ws.Directory(rel).Export(ctx, dest)
		} else {
			exported, err = ws.File(rel).Export(ctx, dest)
		}
		return exportDoneMsg{rel: rel, dest: exported, err: err}
	}
}

func (m *explorerModel) clampScroll() {
	rows := m.paneRows()
	if rows <= 0 {
		return
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
}

// ---------- layout & view ---------------------------------------------------

// treeWidth is the width of the file tree pane; 0 means the tree is hidden
// because the window is too small for a split view.
func (m *explorerModel) treeWidth() int {
	if m.width > 0 && !explorerFits(m.width, m.height) {
		return 0
	}
	w := explorerTreeWidth
	if m.width > 0 && m.width/3 < w {
		w = max(16, m.width/3)
	}
	return w
}

// paneRows is the number of content rows in each pane (total minus the
// header and status lines).
func (m *explorerModel) paneRows() int {
	return max(1, m.height-2)
}

func (m *explorerModel) layoutTerminal() {
	cols := max(10, m.width)
	if tw := m.treeWidth(); tw > 0 {
		cols = max(10, m.width-tw-1)
	}
	rows := m.paneRows()
	if cols == m.termCols && rows == m.termRows {
		return
	}
	m.termCols, m.termRows = cols, rows
	if m.vt == nil {
		m.vt = midterm.NewTerminal(rows, cols)
		// Like a real terminal, start with the cursor shown; programs can
		// still hide it (e.g. vim/less send ESC[?25l).
		m.vt.CursorVisible = true
		// Answer terminal queries (cursor position reports, device
		// attributes, ...) the way a real terminal would. Programs commonly
		// probe the terminal at startup and block until a reply or a timeout
		// (termenv waits 5s), which delayed the shell prompt.
		m.vt.ForwardResponses = stdinQueueWriter{m}
		for _, out := range m.pending {
			_, _ = m.vt.Write(out)
		}
		m.pending = nil
	} else {
		m.vt.Resize(rows, cols)
	}
	_ = m.session.Resize(cols, rows)
}

// sendStdin queues bytes for the session's stdin without blocking the UI.
func (m *explorerModel) sendStdin(b []byte) {
	if m.stdinQ == nil {
		return
	}
	buf := make([]byte, len(b))
	copy(buf, b)
	select {
	case m.stdinQ <- buf:
	default: // session is not draining input; drop rather than stall the UI
	}
}

// stdinQueueWriter lets the vt write query responses into the session's stdin.
type stdinQueueWriter struct{ m *explorerModel }

func (w stdinQueueWriter) Write(p []byte) (int, error) {
	w.m.sendStdin(p)
	return len(p), nil
}

var (
	explorerHeaderStyle   = lipgloss.NewStyle().Bold(true).Reverse(true)
	explorerCursorStyle   = lipgloss.NewStyle().Reverse(true)
	explorerDimStyle      = lipgloss.NewStyle().Faint(true)
	explorerFocusedBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func (m *explorerModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}

	treeW := m.treeWidth()
	rows := m.paneRows()

	left := m.viewLeft(treeW, rows)
	right := m.viewTerminal(rows)

	sep := "│"
	if m.focus == focusTree {
		sep = explorerFocusedBorder.Render("┃")
	}

	var b strings.Builder
	// Header line.
	title := " exec failed — explorer: " + m.workdir + " "
	b.WriteString(truncate.StringWithTail(explorerHeaderStyle.Render(title), uint(m.width), "…"))
	b.WriteString("\n")

	for i := 0; i < rows; i++ {
		if treeW > 0 {
			b.WriteString(left[i])
			b.WriteString(sep)
		}
		if i < len(right) {
			b.WriteString(right[i])
		}
		b.WriteString("\x1b[0m\n")
	}

	// Status line.
	focusHint := "ctrl+q: files • F5: refresh"
	if m.focus == focusTree {
		focusHint = "ctrl+q: terminal • enter: open • o: export • r/F5: refresh • q: quit"
	}
	status := m.status
	if m.loadErr != "" {
		status = m.loadErr
	}
	statusLine := " " + status + "  —  " + focusHint
	if p := m.prompt; p != nil {
		kind := "file"
		if p.isDir {
			kind = "directory"
		}
		b.WriteString(truncate.StringWithTail(
			explorerHeaderStyle.Render(" export "+kind+" "+p.rel+" to: ")+
				" "+string(p.value)+"█  "+
				explorerDimStyle.Render("(enter: export • esc: cancel • ctrl+u: clear)"),
			uint(m.width), "…"))
		return b.String()
	}
	b.WriteString(truncate.StringWithTail(explorerDimStyle.Render(statusLine), uint(m.width), "…"))
	return b.String()
}

func (m *explorerModel) viewLeft(width, rows int) []string {
	if m.previewPath != "" {
		return m.viewPreview(width, rows)
	}
	return m.viewTree(width, rows)
}

func (m *explorerModel) viewTree(width, rows int) []string {
	nodes := m.visibleNodes()
	m.clampScroll()

	lines := make([]string, rows)
	for i := 0; i < rows; i++ {
		idx := m.scroll + i
		if idx >= len(nodes) {
			lines[i] = strings.Repeat(" ", width)
			continue
		}
		n := nodes[idx]
		indent := strings.Repeat("  ", n.depth)
		icon := "  "
		if n.isDir {
			icon = "▸ "
			if n.expanded {
				icon = "▾ "
			}
			if n.loading {
				icon = "⋯ "
			}
		}
		name := n.name
		if n.isDir {
			name += "/"
		}
		line := truncate.StringWithTail(indent+icon+name, uint(width), "…")
		line += strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
		if idx == m.cursor && m.focus == focusTree {
			line = explorerCursorStyle.Render(line)
		}
		lines[i] = line
	}
	return lines
}

func (m *explorerModel) viewPreview(width, rows int) []string {
	lines := make([]string, rows)
	header := truncate.StringWithTail(" "+m.previewPath+" ", uint(width), "…")
	header += strings.Repeat(" ", max(0, width-lipgloss.Width(header)))
	lines[0] = explorerHeaderStyle.Render(header)
	for i := 1; i < rows; i++ {
		idx := m.previewOff + i - 1
		if idx >= len(m.previewText) {
			lines[i] = strings.Repeat(" ", width)
			continue
		}
		line := strings.ReplaceAll(m.previewText[idx], "\t", "    ")
		line = truncate.StringWithTail(line, uint(width), "…")
		line += strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
		lines[i] = line
	}
	return lines
}

func (m *explorerModel) viewTerminal(rows int) []string {
	lines := make([]string, rows)
	if m.vt == nil {
		return lines
	}
	// Show the cursor only while the terminal pane has focus, so it's clear
	// where keys go; render it steady since we only re-render on events (a
	// blinking cursor would randomly be caught in its "off" phase).
	visible, blink := m.vt.CursorVisible, m.vt.CursorBlinkEpoch
	m.vt.CursorVisible = visible && m.focus == focusTerm && !m.exited
	m.vt.CursorBlinkEpoch = nil
	for i := 0; i < rows && i < m.vt.Height; i++ {
		var buf strings.Builder
		_ = m.vt.RenderLine(&buf, i)
		lines[i] = strings.TrimSuffix(buf.String(), "\n")
	}
	m.vt.CursorVisible, m.vt.CursorBlinkEpoch = visible, blink
	return lines
}

// ---------- key encoding ----------------------------------------------------

// keyToBytes converts a bubbletea key event back into the raw bytes a
// terminal would send, for forwarding into the remote PTY.
func keyToBytes(msg tea.KeyMsg) []byte {
	// Ctrl+A..Z and friends map directly to their ASCII control codes.
	if msg.Type > 0 && msg.Type <= 0x1f {
		return []byte{byte(msg.Type)}
	}
	switch msg.Type {
	case tea.KeyRunes:
		s := string(msg.Runes)
		if msg.Alt {
			s = "\x1b" + s
		}
		return []byte(s)
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyEsc:
		return []byte("\x1b")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	}
	return nil
}
