package idtui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/util/patchpreview"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// DiffEntry is one change browsable in the diff viewer: a commit, say, or a
// workspace's uncommitted edits.
type DiffEntry struct {
	// ID identifies the entry across updates, so it stays selected while the
	// list around it changes: a commit SHA, say.
	ID string
	// Version identifies the entry's content: a loaded diff is reused while
	// it is unchanged. Empty means ID, i.e. immutable content like a commit.
	Version string
	// Group is the heading the entry is listed under, e.g. "Commits to save".
	// Consecutive entries share a heading; entries without one are listed
	// bare.
	Group string
	// Label is a short prefix, e.g. an abbreviated SHA.
	Label string
	// Title is a one-line summary, e.g. a commit subject.
	Title string
	// Load fetches the entry's detail. The viewer calls it off the UI
	// goroutine, once per Version while the diff is cached.
	Load func() (DiffDetail, error)
}

func (e DiffEntry) version() string {
	if e.Version != "" {
		return e.Version
	}
	return e.ID
}

// DiffDetail is what the diff viewer shows for an entry.
type DiffDetail struct {
	// Header is plain text shown above the patch, e.g. a commit's author,
	// date and message.
	Header string
	// Patch is a unified Git diff.
	Patch string
}

// diffViewerKey opens the diff viewer from the prompt and from nav mode. Free
// in both: tuist's TextInput leaves ctrl+g unbound.
const diffViewerKey = "ctrl+g"

var diffViewerBinding = key.NewBinding(key.WithKeys(diffViewerKey), key.WithHelp(diffViewerKey, "view diff"))

const (
	// diffTabWidth expands tabs in patches. Narrower than Git's 8 columns:
	// the pane shares the screen with the sidebar.
	diffTabWidth = 4
	// maxSyntaxDiffBytes caps the patch size that is syntax highlighted.
	// Larger patches still get diff coloring, just not a lexer pass.
	maxSyntaxDiffBytes = 1 << 20
	// diffWheelLines is how far a mouse wheel notch scrolls the patch.
	diffWheelLines = 3
)

type diffFocusArea uint8

const (
	diffFocusSidebar diffFocusArea = iota
	diffFocusPatch
)

// DiffViewer is a fullscreen browser for a sidebar section's Diffs: the
// entries in a sidebar on the left, the selected one's syntax-highlighted
// patch on the right -- a sibling of the fullscreen tests view.
//
// It reads the section live, so it follows the bubble as it updates (an agent
// committing while you review), keeping the selection by entry ID.
type DiffViewer struct {
	tuist.Compo

	Profile termenv.Profile
	// Section returns the section being browsed, as it is now.
	Section func() SidebarSection
	// Stale reports whether the section describes something other than what
	// is focused now (another agent's changes, while focus moves), so its
	// entries are withheld until the section catches up.
	Stale func(SidebarSection) bool
	// Dispatch runs a function on the UI goroutine, where loaded diffs land.
	Dispatch func(func())
	// Hint renders a key hint line beneath the panes, or "" for none.
	Hint func(width int) string

	focused bool
	area    diffFocusArea

	selected string
	scroll   int

	// hovered is the sidebar entry under the mouse, or -1.
	hovered int

	details map[string]*diffDetailState

	// Layout of the last render, for key and mouse handling.
	leftWidth  int
	bodyTop    int
	bodyHeight int
	rowByLine  map[int]int
	current    *diffDetailState
}

var (
	_ tuist.Component    = (*DiffViewer)(nil)
	_ tuist.Focusable    = (*DiffViewer)(nil)
	_ tuist.Dismounter   = (*DiffViewer)(nil)
	_ tuist.MouseEnabled = (*DiffViewer)(nil)
	_ tuist.Hoverable    = (*DiffViewer)(nil)
)

// diffDetailState is an entry's loaded (or loading) detail, prepared for
// rendering off the UI goroutine.
type diffDetailState struct {
	loading bool
	err     error

	header []string
	stats  []patchpreview.Entry
	// patch holds the colored patch lines; files the index of each file's
	// "diff --git" header among them.
	patch []string
	files []int

	// body memoizes the scrollable lines at bodyWidth.
	body       []string
	bodyWidth  int
	patchStart int
}

func (v *DiffViewer) Name() string { return "DiffViewer" }

func (v *DiffViewer) SetFocused(_ tuist.Context, focused bool) {
	if v.focused != focused {
		v.focused = focused
		v.Update()
	}
}

func (v *DiffViewer) OnDismount() {
	v.focused = false
}

func (v *DiffViewer) section() SidebarSection {
	if v.Section == nil {
		return SidebarSection{}
	}
	return v.Section()
}

func (v *DiffViewer) stale(sec SidebarSection) bool {
	return v.Stale != nil && v.Stale(sec)
}

func (v *DiffViewer) entries() []DiffEntry {
	sec := v.section()
	if v.stale(sec) {
		return nil
	}
	return sec.Diffs
}

// selectedIndex resolves the selection against the current entries, falling
// back to the first when the selected entry is gone (e.g. its uncommitted
// edits were committed).
func (v *DiffViewer) selectedIndex(entries []DiffEntry) int {
	if len(entries) == 0 {
		return -1
	}
	for i, entry := range entries {
		if entry.ID == v.selected {
			return i
		}
	}
	v.selected = entries[0].ID
	v.scroll = 0
	return 0
}

func (v *DiffViewer) selectIndex(entries []DiffEntry, idx int) {
	if len(entries) == 0 {
		return
	}
	idx = max(0, min(idx, len(entries)-1))
	if entries[idx].ID != v.selected {
		v.selected = entries[idx].ID
		v.scroll = 0
	}
	v.Update()
}

// SelectBy moves the selection by delta entries.
func (v *DiffViewer) SelectBy(delta int) {
	entries := v.entries()
	v.selectIndex(entries, v.selectedIndex(entries)+delta)
}

// Up and Down select entries from the sidebar, and scroll the patch from it.
func (v *DiffViewer) Up() {
	if v.area == diffFocusPatch {
		v.ScrollBy(-1)
		return
	}
	v.SelectBy(-1)
}

func (v *DiffViewer) Down() {
	if v.area == diffFocusPatch {
		v.ScrollBy(1)
		return
	}
	v.SelectBy(1)
}

func (v *DiffViewer) Home() {
	if v.area == diffFocusPatch {
		v.ScrollTo(0)
		return
	}
	v.selectIndex(v.entries(), 0)
}

func (v *DiffViewer) End() {
	if v.area == diffFocusPatch {
		v.ScrollTo(v.maxScroll())
		return
	}
	entries := v.entries()
	v.selectIndex(entries, len(entries)-1)
}

func (v *DiffViewer) ScrollBy(delta int) {
	v.ScrollTo(v.scroll + delta)
}

func (v *DiffViewer) ScrollPage(pages int) {
	v.ScrollBy(pages * max(v.bodyHeight-1, 1))
}

func (v *DiffViewer) ScrollTo(line int) {
	line = max(0, min(line, v.maxScroll()))
	if line != v.scroll {
		v.scroll = line
		v.Update()
	}
}

func (v *DiffViewer) maxScroll() int {
	if v.current == nil || v.current.body == nil {
		return 0
	}
	return max(len(v.current.body)-v.bodyHeight, 0)
}

// NextFile and PrevFile scroll the patch to the next or previous file.
func (v *DiffViewer) NextFile() {
	st := v.current
	if st == nil {
		return
	}
	for _, idx := range st.files {
		if line := st.patchStart + idx; line > v.scroll {
			v.ScrollTo(line)
			return
		}
	}
}

func (v *DiffViewer) PrevFile() {
	st := v.current
	if st == nil {
		return
	}
	for i := len(st.files) - 1; i >= 0; i-- {
		if line := st.patchStart + st.files[i]; line < v.scroll {
			v.ScrollTo(line)
			return
		}
	}
}

// FocusPatch and FocusSidebar move keyboard focus between the panes.
func (v *DiffViewer) FocusPatch() {
	if v.area != diffFocusPatch && len(v.entries()) > 0 {
		v.area = diffFocusPatch
		v.Update()
	}
}

func (v *DiffViewer) FocusSidebar() {
	if v.area != diffFocusSidebar {
		v.area = diffFocusSidebar
		v.Update()
	}
}

func (v *DiffViewer) PatchFocused() bool {
	return v.area == diffFocusPatch
}

func (v *DiffViewer) HasFiles() bool {
	return v.current != nil && len(v.current.files) > 1
}

// load returns the detail state for entry, starting its load if needed.
func (v *DiffViewer) load(entry DiffEntry) *diffDetailState {
	if v.details == nil {
		v.details = make(map[string]*diffDetailState)
	}
	key := entry.version()
	if st := v.details[key]; st != nil {
		return st
	}
	st := &diffDetailState{loading: entry.Load != nil}
	v.details[key] = st
	if entry.Load == nil {
		return st
	}
	profile := v.Profile
	load := entry.Load
	dispatch := v.Dispatch
	go func() {
		detail, err := load()
		prepared := prepareDiffDetail(profile, detail, err)
		apply := func() {
			if v.details[key] != st {
				return // evicted while loading
			}
			*st = *prepared
			v.Update()
		}
		if dispatch == nil {
			apply()
			return
		}
		dispatch(apply)
	}()
	return st
}

// prune evicts details that no current entry refers to.
func (v *DiffViewer) prune(entries []DiffEntry) {
	for key := range v.details {
		if !slices.ContainsFunc(entries, func(e DiffEntry) bool { return e.version() == key }) {
			delete(v.details, key)
		}
	}
}

func (v *DiffViewer) Render(ctx tuist.Context) {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(v.Profile))
	width := max(ctx.Width, 1)
	height := ctx.Height
	if height <= 0 {
		height = max(ctx.ScreenHeight()-2, 1)
	}
	var hint string
	if v.Hint != nil && height > 1 {
		hint = v.Hint(width)
		if hint != "" {
			height--
		}
	}

	sec := v.section()
	stale := v.stale(sec)
	entries := sec.Diffs
	if stale {
		// Another agent's changes: keep their diffs cached, but show none.
		entries = nil
	} else {
		v.prune(entries)
	}
	idx := v.selectedIndex(entries)
	if idx < 0 {
		v.area = diffFocusSidebar
	}

	leftWidth, rightWidth := splitPaneWidths(width)
	v.leftWidth = leftWidth
	left := v.renderSidebar(out, sec, entries, idx, stale, leftWidth, height)
	right := v.renderPatch(out, sec, entries, idx, stale, rightWidth, height)
	for i := range height {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		ctx.Line(renderTestPaneLine(out, l, r, leftWidth))
	}
	if hint != "" {
		ctx.Line(hint)
	}
}

// splitPaneWidths divides width between a sidebar and a detail pane, with a
// three-column gutter between them.
func splitPaneWidths(width int) (int, int) {
	leftWidth := width / 3
	if leftWidth < 28 {
		leftWidth = min(width, 28)
	}
	if leftWidth > 44 {
		leftWidth = 44
	}
	rightWidth := width - leftWidth - 3
	if rightWidth < 20 && width > 24 {
		leftWidth = max(width-23, 12)
		rightWidth = width - leftWidth - 3
	}
	return leftWidth, max(rightWidth, 1)
}

// diffSidebarRow is a sidebar line: a group heading, or an entry.
type diffSidebarRow struct {
	heading string
	count   int
	entry   int
}

func diffSidebarRows(entries []DiffEntry) []diffSidebarRow {
	var rows []diffSidebarRow
	for i, entry := range entries {
		if entry.Group != "" && (i == 0 || entries[i-1].Group != entry.Group) {
			count := 0
			for _, e := range entries[i:] {
				if e.Group != entry.Group {
					break
				}
				count++
			}
			rows = append(rows, diffSidebarRow{heading: entry.Group, count: count, entry: -1})
		}
		rows = append(rows, diffSidebarRow{entry: i})
	}
	return rows
}

func (v *DiffViewer) renderSidebar(out *termenv.Output, sec SidebarSection, entries []DiffEntry, selected int, stale bool, width, height int) []string {
	faint := func(s string) string {
		return out.String(s).Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	title := strings.ToUpper(sec.Title)
	if title == "" {
		title = "CHANGES"
	}
	heading := out.String(title).Bold().String()
	if len(entries) > 0 {
		heading += "  " + faint(fmt.Sprint(len(entries)))
	}
	lines := []string{
		tuist.Truncate(heading, width, "…"),
		faint(strings.Repeat(HorizBar, max(width, 0))),
	}
	v.rowByLine = make(map[int]int)
	listHeight := height - len(lines)
	if len(entries) == 0 {
		empty := "No changes"
		if stale {
			empty = "Loading…"
		}
		lines = append(lines, faint(clipPlain(empty, width)))
		return cropLines(lines, height)
	}
	if listHeight <= 0 {
		return cropLines(lines, height)
	}

	rows := diffSidebarRows(entries)
	selectedRow := slices.IndexFunc(rows, func(row diffSidebarRow) bool { return row.entry == selected })
	// Keep a group's heading in view along with its first entry, when the
	// window still shows the entry itself.
	start, end, top, bottom := scrollWindow(len(rows), selectedRow, listHeight)
	if selectedRow > 0 && rows[selectedRow-1].entry < 0 {
		if s, e, t, b := scrollWindow(len(rows), selectedRow-1, listHeight); selectedRow < e {
			start, end, top, bottom = s, e, t, b
		}
	}
	if top {
		lines = append(lines, faint(clipPlain(fmt.Sprintf("… %d above", start), width)))
	}
	for i := start; i < end; i++ {
		row := rows[i]
		if row.entry < 0 {
			label := out.String(clipPlain(row.heading, max(width-len(fmt.Sprint(row.count))-1, 1))).Bold().String()
			lines = append(lines, label+" "+faint(fmt.Sprint(row.count)))
			continue
		}
		v.rowByLine[len(lines)] = row.entry
		lines = append(lines, v.renderSidebarEntry(out, entries[row.entry], row.entry == selected, row.entry == v.hovered, width))
	}
	if bottom {
		lines = append(lines, faint(clipPlain(fmt.Sprintf("… %d more", len(rows)-end), width)))
	}
	return cropLines(lines, height)
}

func (v *DiffViewer) renderSidebarEntry(out *termenv.Output, entry DiffEntry, selected, hovered bool, width int) string {
	selector := " "
	if selected {
		selector = CaretRightFilled
	}
	label := ""
	if entry.Label != "" {
		label = entry.Label + " "
	}
	const gutter = 2 // selector + space
	label = clipPlain(label, max(width-gutter, 0))
	title := clipPlain(sanitizeDiffLine(entry.Title), max(width-gutter-tuist.VisibleWidth(label), 0))
	if !selected && !hovered {
		return selector + " " + out.String(label).Foreground(termenv.ANSIYellow).String() + title
	}
	bold := selected && v.area == diffFocusSidebar
	var b strings.Builder
	b.WriteString(sidebarSelectedSegment(out, selector+" ", termenv.ANSIWhite, bold, false))
	b.WriteString(sidebarSelectedSegment(out, label, termenv.ANSIYellow, false, false))
	b.WriteString(sidebarSelectedSegment(out, title, termenv.ANSIWhite, bold, false))
	if pad := width - gutter - tuist.VisibleWidth(label) - tuist.VisibleWidth(title); pad > 0 {
		b.WriteString(sidebarSelectedSegment(out, strings.Repeat(" ", pad), nil, false, false))
	}
	return b.String()
}

// scrollWindow picks the rows [start, end) of n to show in height lines with
// selected visible. top and bottom report "more" markers, each taking a line.
func scrollWindow(n, selected, height int) (start, end int, top, bottom bool) {
	if n <= height {
		return 0, n, false, false
	}
	if selected >= height {
		start = selected - height/2
	}
	start = max(0, min(start, n-height))
	for {
		top = start > 0
		slots := height
		if top {
			slots--
		}
		bottom = start+max(slots, 0) < n
		if bottom {
			slots--
		}
		if slots < 1 {
			top, bottom = false, false
			slots = max(height, 1)
		}
		end = min(start+slots, n)
		if selected < start && start > 0 {
			start--
			continue
		}
		if selected >= end && end < n {
			start++
			continue
		}
		return start, end, top, bottom
	}
}

func (v *DiffViewer) renderPatch(out *termenv.Output, sec SidebarSection, entries []DiffEntry, selected int, stale bool, width, height int) []string {
	faint := func(s string) string {
		return out.String(s).Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	rule := faint(strings.Repeat(HorizBar, max(width, 0)))
	v.current = nil
	v.bodyTop = 2
	v.bodyHeight = max(height-2, 0)
	if stale {
		lines := []string{out.String(clipPlain("Loading changes…", width)).Bold().String(), rule}
		return cropLines(lines, height)
	}
	if selected < 0 {
		// Nothing to browse. Show what the bubble says instead, e.g. that a
		// save is in progress or why the changes could not be computed.
		lines := []string{out.String(clipPlain("No changes to review", width)).Bold().String(), rule}
		if body := strings.TrimSpace(sec.Body(width)); body != "" {
			for _, line := range strings.Split(body, "\n") {
				lines = append(lines, tuist.Truncate(line, width, "…"))
			}
		}
		return cropLines(lines, height)
	}

	entry := entries[selected]
	st := v.load(entry)
	// Prefetch the neighbours, so stepping through the list is instant.
	if selected > 0 {
		v.load(entries[selected-1])
	}
	if selected+1 < len(entries) {
		v.load(entries[selected+1])
	}
	v.current = st
	body := st.bodyLines(v.Profile, width)
	v.scroll = max(0, min(v.scroll, max(len(body)-v.bodyHeight, 0)))

	position := ""
	if len(body) > v.bodyHeight && v.bodyHeight > 0 {
		position = fmt.Sprintf(" %d-%d/%d", v.scroll+1, min(v.scroll+v.bodyHeight, len(body)), len(body))
	}
	titleText := sanitizeDiffLine(entry.Title)
	if entry.Label != "" {
		titleText = entry.Label + " " + titleText
	}
	titleText = clipPlain(titleText, max(width-len(position), 1))
	var title string
	if v.area == diffFocusPatch {
		title = out.String(padANSI(titleText+position, width)).Foreground(termenv.ANSIWhite).Background(testSidebarRowBG).Bold().String()
	} else {
		title = out.String(titleText).Bold().String() + faint(position)
	}
	lines := []string{title, rule}
	for _, line := range body[v.scroll:min(v.scroll+v.bodyHeight, len(body))] {
		line = tuist.Truncate(line, width, "…")
		if strings.Contains(line, "\x1b") {
			// A token spanning lines (a block comment, say) leaves its color
			// open; don't let it bleed into the next row's sidebar.
			line += "\x1b[0m"
		}
		lines = append(lines, line)
	}
	return cropLines(lines, height)
}

// bodyLines lays out the detail's scrollable lines for width.
func (st *diffDetailState) bodyLines(profile termenv.Profile, width int) []string {
	if st.body != nil && st.bodyWidth == width {
		return st.body
	}
	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(profile))
	faint := func(s string) string {
		return out.String(s).Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	var body []string
	switch {
	case st.loading:
		body = append(body, faint("Loading diff…"))
	case st.err != nil:
		for _, line := range strings.Split(sanitizeDiffText("failed to load diff: "+st.err.Error()), "\n") {
			body = append(body, out.String(line).Foreground(termenv.ANSIRed).String())
		}
	default:
		body = append(body, st.header...)
		if len(st.stats) > 0 {
			if len(body) > 0 {
				body = append(body, "")
			}
			patchpreview.Summarize(out, slices.Clone(st.stats), width)
			body = append(body, strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")...)
		}
		if len(body) > 0 {
			body = append(body, "")
		}
		st.patchStart = len(body)
		if len(st.patch) == 0 {
			body = append(body, faint("No changes."))
		} else {
			body = append(body, st.patch...)
		}
	}
	st.body = body
	st.bodyWidth = width
	return body
}

// prepareDiffDetail sanitizes and colors a loaded detail. It runs off the UI
// goroutine: syntax highlighting a large patch takes a while.
func prepareDiffDetail(profile termenv.Profile, detail DiffDetail, err error) *diffDetailState {
	st := &diffDetailState{err: err}
	if err != nil {
		return st
	}
	if header := strings.TrimRight(sanitizeDiffText(detail.Header), "\n "); header != "" {
		for _, line := range strings.Split(header, "\n") {
			st.header = append(st.header, tuist.ExpandTabs(line, diffTabWidth))
		}
	}
	patch := strings.TrimRight(sanitizeDiffText(detail.Patch), "\n")
	if patch == "" {
		return st
	}
	raw := strings.Split(patch, "\n")
	for i, line := range raw {
		if strings.HasPrefix(line, "diff --git ") {
			st.files = append(st.files, i)
		}
		raw[i] = tuist.ExpandTabs(line, diffTabWidth)
	}
	st.stats = diffStatEntries(raw)
	colored := strings.Split(colorizeDiff(profile, strings.Join(raw, "\n"), len(patch) <= maxSyntaxDiffBytes), "\n")
	if len(colored) != len(raw) {
		colored = raw // never misalign the file offsets
	}
	st.patch = colored
	return st
}

// diffStatEntries tallies a patch's per-file changes for the summary above it.
func diffStatEntries(lines []string) []patchpreview.Entry {
	var entries []patchpreview.Entry
	cur := -1
	inHunk := false
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			entries = append(entries, patchpreview.Entry{Path: diffGitPath(line), Kind: patchpreview.KindModified})
			cur = len(entries) - 1
			inHunk = false
			continue
		}
		if cur < 0 {
			continue
		}
		entry := &entries[cur]
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			continue
		}
		if inHunk {
			if line == "" {
				continue
			}
			switch line[0] {
			case '+':
				entry.Added++
				continue
			case '-':
				entry.Removed++
				continue
			case ' ', '\\':
				continue
			}
			inHunk = false
		}
		switch {
		case strings.HasPrefix(line, "new file mode "):
			entry.Kind = patchpreview.KindAdded
		case strings.HasPrefix(line, "deleted file mode "):
			entry.Kind = patchpreview.KindRemoved
		case strings.HasPrefix(line, "rename from "):
			entry.Kind = patchpreview.KindRenamed
			entry.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			entry.Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "+++ "):
			if path := diffHeaderPath(line[4:]); path != "" {
				entry.Path = path
			}
		}
	}
	return entries
}

// sanitizeDiffText neutralizes control characters in repository data (file
// contents, commit messages) so they are displayed, never interpreted by the
// terminal. Newlines and tabs are kept for layout; CRLF becomes LF.
func sanitizeDiffText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, s)
}

// sanitizeDiffLine is sanitizeDiffText for a single line.
func sanitizeDiffLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (v *DiffViewer) HandleMouse(ctx tuist.Context, ev tuist.MouseEvent) bool {
	onSidebar := ev.Col < v.leftWidth
	switch ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		hovered := -1
		if idx, ok := v.rowByLine[ev.Row]; ok && onSidebar {
			hovered = idx
		}
		v.setHovered(hovered)
		return true
	case uv.MouseWheelEvent:
		delta := 1
		if ev.Mouse().Button == uv.MouseWheelUp {
			delta = -1
		}
		if onSidebar {
			v.SelectBy(delta)
		} else {
			v.ScrollBy(delta * diffWheelLines)
		}
		return true
	case uv.MouseClickEvent:
		if ev.Mouse().Button != uv.MouseLeft {
			return true
		}
		if !onSidebar {
			v.FocusPatch()
			return true
		}
		if idx, ok := v.rowByLine[ev.Row]; ok {
			v.area = diffFocusSidebar
			v.selectIndex(v.entries(), idx)
		}
		return true
	}
	return false
}

// SetHovered clears the hover highlight when the mouse leaves the viewer.
func (v *DiffViewer) SetHovered(_ tuist.Context, hovered bool) {
	if !hovered {
		v.setHovered(-1)
	}
}

func (v *DiffViewer) setHovered(idx int) {
	if v.hovered != idx {
		v.hovered = idx
		v.Update()
	}
}

// ---------- frontend integration --------------------------------------------

// diffSectionTitle names the HUD section whose diffs ctrl+g browses: the
// first, in HUD order, with any.
func (fe *frontendPretty) diffSectionTitle() string {
	if fe.notificationContainer == nil {
		return ""
	}
	for _, child := range fe.notificationContainer.Children {
		if bubble, ok := child.(*NotificationBubble); ok && bubble != fe.keymapBubble && len(bubble.section.Diffs) > 0 {
			return bubble.section.Title
		}
	}
	return ""
}

func (fe *frontendPretty) hasDiffs() bool {
	return fe.diffSectionTitle() != ""
}

func (fe *frontendPretty) diffViewerFocused() bool {
	return fe.tui != nil && fe.diffViewer != nil && fe.tui.IsFocused(fe.diffViewer)
}

// toggleDiffViewer opens the diff viewer on the HUD's diffs, or closes it.
// It reports whether it did either.
func (fe *frontendPretty) toggleDiffViewer() bool {
	if fe.diffViewer != nil {
		fe.closeDiffViewer()
		return true
	}
	if fe.testsMode || fe.logPager != nil {
		return false
	}
	title := fe.diffSectionTitle()
	if title == "" {
		return false
	}
	fe.diffViewer = &DiffViewer{
		Profile: fe.profile,
		Section: func() SidebarSection {
			if bubble := fe.notifications[title]; bubble != nil {
				return bubble.section
			}
			return SidebarSection{Title: title}
		},
		Stale:    fe.sectionStale,
		Dispatch: fe.dispatch,
		Hint:     fe.diffViewerHint,
		hovered:  -1,
	}
	fe.diffViewerTitle = title
	// Hiding the prompt dismounts its input, and tuist won't restore focus to
	// an unmounted component; remember to hand it back on close.
	fe.diffViewerFromInput = fe.inputFocused()
	fe.diffViewerFocus = fe.tui.PushFocus(fe.diffViewer)
	fe.syncPromptHidden()
	// The HUD's bubbles would cover the patch, and the one being browsed is
	// what the viewer shows anyway. The HUD keys still reveal it.
	if fe.notificationOverlay != nil {
		fe.notificationOverlay.SetHidden(true)
	}
	fe.syncHardwareCursor()
	fe.refreshKeymap()
	fe.Update()
	return true
}

func (fe *frontendPretty) closeDiffViewer() {
	if fe.diffViewer == nil {
		return
	}
	fe.diffViewerFocus.Restore()
	fe.diffViewerFocus = nil
	fe.diffViewer = nil
	fe.diffViewerTitle = ""
	fe.syncPromptHidden()
	if fe.notificationOverlay != nil {
		fe.notificationOverlay.SetHidden(fe.notificationsHidden)
	}
	fromInput := fe.diffViewerFromInput
	fe.diffViewerFromInput = false
	if fe.tui.Focused() == nil {
		if fromInput && fe.textInput != nil {
			fe.enterInsertMode()
		} else {
			fe.focusNavigationTarget()
		}
	}
	fe.syncHardwareCursor()
	fe.refreshKeymap()
	fe.Update()
}

// updateDiffViewer re-renders the viewer when the section it browses changes.
func (fe *frontendPretty) updateDiffViewer(title string) {
	if fe.diffViewer != nil && fe.diffViewerTitle == title {
		fe.diffViewer.Update()
	}
}

// promptHidden reports whether the prompt makes way for the diff viewer,
// which owns the keyboard while it is open.
func (fe *frontendPretty) promptHidden() bool {
	return fe.diffViewer != nil
}

// syncPromptHidden re-renders the prompt after the viewer opens or closes.
func (fe *frontendPretty) syncPromptHidden() {
	if fe.promptFrame != nil {
		fe.promptFrame.Update()
	}
	if fe.queuedMsgLabel != nil {
		fe.queuedMsgLabel.Update()
	}
}

// sectionStale reports whether sec, a section describing an agent (see
// SidebarSection.Agent), still describes the agent focus just moved away
// from. The session repaints the focused agent's sections asynchronously
// after a switch; until one naming the new agent arrives, whatever is there
// -- the old agent's, or a section naming no agent at all -- must not pass
// for the new one's. When a switch fails and focus rolls back, the focused
// agent is no longer the one awaited, and the section stands. (Paints from
// an agent that is not focused never land at all; see SetSidebarContent.)
func (fe *frontendPretty) sectionStale(sec SidebarSection) bool {
	return fe.sectionAwaitAgent != "" &&
		fe.sectionAwaitAgent == fe.focusedAgentID() &&
		sec.Agent != fe.sectionAwaitAgent
}

func (fe *frontendPretty) renderDiffViewer(ctx tuist.Context) {
	if fe.diffViewer == nil {
		return
	}
	// Leave room for the siblings still shown beneath: the prompt, status
	// line and keymap bar.
	reserved := fe.keymapHeight() + fe.errorLabelHeight() + fe.queuedMessageHeight() +
		fe.statusLineHeight() + fe.editlineHeight() + fe.formHeight()
	height := ctx.ScreenHeight() - reserved
	if height <= 0 {
		height = max(ctx.Height, 1)
	}
	fe.RenderChild(ctx.Resize(ctx.Width, height), fe.diffViewer)
}

// diffViewerHint renders the viewer's keys beneath it when there is no keymap
// bar to list them (the shell hides it).
func (fe *frontendPretty) diffViewerHint(width int) string {
	if !fe.keymapBarHidden() || fe.diffViewer == nil {
		return ""
	}
	var hint strings.Builder
	RenderKeymap(&hint, KeymapStyle, fe.diffViewerKeys(""), fe.pressedKey, fe.pressedKeyAt)
	return tuist.Truncate(hint.String(), width, "…")
}

// diffViewerKeys lists the viewer's keys, followed by the browsed section's
// own (e.g. save), which keep working inside it.
func (fe *frontendPretty) diffViewerKeys(quitMsg string) []key.Binding {
	v := fe.diffViewer
	patch := v != nil && v.PatchFocused()
	hasEntries := v != nil && len(v.entries()) > 0
	upDown := "select"
	if patch {
		upDown = "scroll"
	}
	binds := []key.Binding{
		key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"),
			key.WithHelp("↑↓", upDown),
			KeyEnabled(hasEntries)),
		key.NewBinding(key.WithKeys("enter", "right", "l"),
			key.WithHelp("enter", "read diff"),
			KeyEnabled(hasEntries && !patch)),
		key.NewBinding(key.WithKeys("left", "h"),
			key.WithHelp("←", "list"),
			KeyEnabled(patch)),
		key.NewBinding(key.WithKeys("[", "]"),
			key.WithHelp("[/]", "prev/next"),
			KeyEnabled(hasEntries && patch)),
		key.NewBinding(key.WithKeys("pgup", "pgdown", "space"),
			key.WithHelp("pgup/dn", "page"),
			KeyEnabled(hasEntries)),
		key.NewBinding(key.WithKeys("n", "N"),
			key.WithHelp("n/N", "next/prev file"),
			KeyEnabled(v != nil && v.HasFiles())),
		key.NewBinding(key.WithKeys("q", "esc", "alt+esc", diffViewerKey),
			key.WithHelp("q", "close")),
	}
	if quitMsg != "" {
		binds = append(binds, key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", quitMsg)))
	}
	if v != nil {
		binds = append(binds, v.section().KeyMap...)
	}
	// The viewer follows the focused agent, so prompt mode's agent keys
	// switch whose changes it shows.
	binds = append(binds, fe.promptAgentBindings()...)
	return binds
}

// handleDiffViewerKey handles nav keys while the diff viewer is open.
func (fe *frontendPretty) handleDiffViewerKey(ev uv.KeyPressEvent, keyStr string) {
	v := fe.diffViewer
	switch keyStr {
	case "q", "esc", "alt+esc", diffViewerKey:
		fe.closeDiffViewer()
	case "ctrl+c":
		if fe.shell != nil {
			fe.interruptCurrent()
		} else {
			fe.quitAction(ErrInterrupted)
		}
	case "up", "k":
		v.Up()
	case "down", "j":
		v.Down()
	case "home", "g":
		v.Home()
	case "end", "G":
		v.End()
	case "pgup", "ctrl+b":
		v.ScrollPage(-1)
	case "pgdown", "ctrl+f", "space":
		v.ScrollPage(1)
	case "enter", "right", "l":
		v.FocusPatch()
	case "left", "h":
		v.FocusSidebar()
	case "tab":
		if v.PatchFocused() {
			v.FocusSidebar()
		} else {
			v.FocusPatch()
		}
	case "[":
		v.SelectBy(-1)
	case "]":
		v.SelectBy(1)
	case "n":
		v.NextFile()
	case "N":
		v.PrevFile()
	case "alt+[", "alt+]":
		// Prompt mode's agent keys, so the viewer can follow another agent's
		// changes without closing. (Bare [ and ] step through entries here.)
		delta := 1
		if keyStr == "alt+[" {
			delta = -1
		}
		fe.cycleAgent(delta)
	default:
		// ctrl+1…9 and alt+l, as in prompt mode.
		if fe.handleAgentFocusShortcut(keyStr) {
			break
		}
		// The section's own keys act on what is being reviewed, e.g. saving
		// the changes to the checkout. They are the shell's to handle.
		if fe.shell == nil || !slices.ContainsFunc(v.section().KeyMap, func(b key.Binding) bool {
			return slices.Contains(b.Keys(), keyStr)
		}) {
			return
		}
		if work := fe.shell.ReactToInput(fe.shellCtx, ev, "", false); work != nil {
			fe.runShellAsync(work)
		}
	}
	fe.refreshKeymap()
}
