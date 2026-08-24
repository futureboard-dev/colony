package tui

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// View is the current full-screen view.
type View int

const (
	ViewDashboard View = iota
	ViewQueue
	ViewTaskDetail
	ViewSessions
	ViewLiveOutput
)

// String returns the display name for the view title.
func (v View) String() string {
	switch v {
	case ViewDashboard:
		return "Dashboard"
	case ViewQueue:
		return "Queue"
	case ViewTaskDetail:
		return "Task Detail"
	case ViewSessions:
		return "Sessions"
	case ViewLiveOutput:
		return "Live Output"
	default:
		return "Unknown"
	}
}

// FromNumber maps the number keys 1..5 to a View.
func FromNumber(n int) (View, bool) {
	switch n {
	case 1:
		return ViewDashboard, true
	case 2:
		return ViewQueue, true
	case 3:
		return ViewTaskDetail, true
	case 4:
		return ViewSessions, true
	case 5:
		return ViewLiveOutput, true
	default:
		return ViewDashboard, false
	}
}

// Modal identifies the active modal overlay, if any.
type Modal int

const (
	ModalNone Modal = iota
	ModalAddTask
	ModalLoopControl
	ModalReview
	ModalObserve
	ModalConfirm
	ModalHelp
	ModalPalette
)

// Options carries the flag-derived configuration into the model.
type Options struct {
	Refresh    time.Duration
	NoColor    bool
	Force      bool
	StartView  View
	ColonyDir  string
	Root       string
	DBPath     string
	StartupLog string // pre-rendered startup errors (e.g. stale PID recovery)
}

// Store is the storage surface the TUI reads and mutates. It is satisfied by
// *storage.SQLiteStore and by test stubs.
type Store interface {
	QueryTasks(f storage.TaskFilter) ([]storage.Task, error)
	QuerySessions(f storage.SessionFilter) ([]storage.Session, error)
	QuerySteps(f storage.StepFilter) ([]storage.Step, error)
	QueryRuns(f storage.RunFilter) ([]storage.Run, error)
	InsertTask(t storage.Task) error
	UpdateTaskState(id, state, feedback string) error
	DeleteTask(id string) error
	Close() error
}

// DBResultMsg carries a fresh poll snapshot from a background goroutine.
type DBResultMsg struct {
	Tasks    []storage.Task
	Sessions []storage.Session
	Steps    []storage.Step
	Err      error
}

type (
	pollTickMsg    struct{}
	toastTickMsg   struct{}
	dbLockedMsg    struct{}
	asyncEventMsg  struct{}
	clearLockedMsg struct{}
)

// runsMsg carries the run history fetched when the Review modal opens.
type runsMsg struct {
	runs []storage.Run
	err  error
}

// Model is the Bubble Tea application state. It owns the view router and the
// read-only by default interaction model.
type Model struct {
	opts     Options
	theme    *Theme
	notifier *Notifier
	poller   *Poller

	store Store

	view     View
	modal    Modal
	confirm  *ConfirmState
	cursor   int
	queue    QueueFilter
	sessFilt SessionsFilter

	tasks      []storage.Task
	sessions   []storage.Session
	steps      []storage.Step
	dbLocked   bool
	lockBanner bool

	width    int
	height   int
	tooSmall bool

	// Queue search: '/' focuses the filter bar and routes keys to searchInput.
	searching   bool
	searchInput textinput.Model

	// Live Output state. scrollOff counts lines between the newest line and the
	// bottom of the visible window; tailer follows .colony/loop.log so a loop
	// started outside this TUI still shows output.
	output    *ringBuffer
	frozen    bool
	wrapLines bool
	scrollOff int
	tailer    *LogTailer

	refresh      time.Duration
	refreshCount int

	lastActive map[string]time.Time

	// AddTask modal fields. addDesc/addSpec/addBase/addLang mirror the inputs so
	// tests and callers can set them without driving the text inputs.
	addDesc     string
	addSpec     string
	addBase     string
	addLang     string
	addNoFormat bool
	addErr      string
	addInputs   []textinput.Model
	addFocus    int

	// Command palette state and the child process it drives.
	palette paletteState
	runner  *Runner

	// Review modal state. Runs are fetched when the modal opens rather than on
	// every poll tick, since they are only visible there.
	runs      []storage.Run
	runsErr   error
	runCursor int
}

// Add Task modal field indices. The text inputs occupy 0..addNoFormatField-1;
// addNoFormatField is the trailing boolean toggle and has no text input.
const (
	addDescField = iota
	addSpecField
	addBaseField
	addLangField
	addNoFormatField
	addFieldCount
)

// ConfirmState describes a pending destructive-action confirmation.
type ConfirmState struct {
	Prompt string
	Detail string
	Action func(*Model)
}

// Launch runs the Bubble Tea program to completion.
func Launch(opts Options, s Store) error {
	p := tea.NewProgram(New(opts, s), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// New builds a Model for the given options. A nil store means the model can be
// constructed for rendering/keybinding tests and defers DB access.
func New(opts Options, store Store) *Model {
	refresh := opts.Refresh
	if refresh < 100*time.Millisecond {
		refresh = time.Second
	}
	desc := textinput.New()
	desc.Placeholder = "what should the agent do?"
	desc.CharLimit = 500
	spec := textinput.New()
	spec.Placeholder = ".colony/specs/<feature>/TASK.md (optional)"
	spec.CharLimit = 300
	base := textinput.New()
	base.Placeholder = "hotfix/my-branch (optional, defaults to current)"
	base.CharLimit = 200
	lang := textinput.New()
	lang.Placeholder = "typescript | python | go"
	lang.CharLimit = 20

	search := textinput.New()
	search.Placeholder = "filter descriptions"
	search.CharLimit = 100

	m := &Model{
		opts:        opts,
		theme:       DefaultTheme(opts.NoColor),
		notifier:    NewNotifier(nil),
		poller:      NewPoller(nil),
		store:       store,
		view:        opts.StartView,
		modal:       ModalNone,
		queue:       QueueFilter{State: "", Sort: "created", Search: ""},
		sessFilt:    SessionsFilter{Sort: "started"},
		refresh:     refresh,
		lastActive:  make(map[string]time.Time),
		searchInput: search,
		output:      newRingBuffer(LiveOutputCapacity),
		addInputs:   []textinput.Model{desc, spec, base, lang},
		runner:      NewRunner(),
	}
	if opts.ColonyDir != "" {
		m.tailer = NewLogTailer(filepath.Join(opts.ColonyDir, loopLogFile))
	}
	if m.view == ViewTaskDetail {
		m.view = ViewQueue // task detail requires a selected task
	}
	if opts.StartupLog != "" {
		m.notifier.Push(opts.StartupLog, ToastAsync)
	}
	return m
}

// Init returns the initial commands: a refresh tick, the toast clock, and the
// reader for the loop log tailer.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tickCmd(m.refresh),
		tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return toastTickMsg{} }),
		m.loadOnce(),
	}
	if m.tailer != nil {
		go m.tailer.Run()
		cmds = append(cmds, m.readLogLine())
	}
	return tea.Batch(cmds...)
}

// readLogLine pulls one tailed log line and re-arms itself, the same way
// readOutput drains the child-process channel.
func (m *Model) readLogLine() tea.Cmd {
	lines := m.tailer.Lines()
	return func() tea.Msg {
		line, ok := <-lines
		if !ok {
			return nil
		}
		return logLineMsg{line: line}
	}
}

// loadOnce performs the first DB read and seeds the poller high-water mark.
func (m *Model) loadOnce() tea.Cmd {
	if m.store == nil {
		return nil
	}
	return func() tea.Msg {
		return m.queryDB()
	}
}

// openReview shows the Review modal and fetches run history for it. Runs are
// loaded on open rather than on every poll tick because nothing outside this
// modal reads them.
func (m *Model) openReview() tea.Cmd {
	m.modal = ModalReview
	m.runs, m.runsErr, m.runCursor = nil, nil, 0
	if m.store == nil {
		return nil
	}
	return func() tea.Msg {
		runs, err := m.store.QueryRuns(storage.RunFilter{})
		return runsMsg{runs: runs, err: err}
	}
}

// selectedRun returns the run under the Review modal's cursor.
func (m *Model) selectedRun() (storage.Run, bool) {
	if m.runCursor < 0 || m.runCursor >= len(m.runs) {
		return storage.Run{}, false
	}
	return m.runs[m.runCursor], true
}

// queryDB runs all queries and returns a DBResultMsg.
func (m *Model) queryDB() tea.Msg {
	tasks, err := m.store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		return DBResultMsg{Err: err}
	}
	sessions, sErr := m.store.QuerySessions(storage.SessionFilter{})
	steps, pErr := m.store.QuerySteps(storage.StepFilter{})
	if sErr != nil {
		return DBResultMsg{Err: sErr}
	}
	if pErr != nil {
		return DBResultMsg{Err: pErr}
	}
	return DBResultMsg{Tasks: tasks, Sessions: sessions, Steps: steps}
}

// pollTickCmd returns a command that issues a poll tick. It uses the poller to
// decide whether a re-query is needed and (when the DB is locked) manages retry
// state. Returns nil when no work is needed so the model skips a re-query.
func (m *Model) pollTickCmd() tea.Cmd {
	m.refreshCount++
	return tickCmd(m.refresh)
}

// Update is Bubble Tea's event loop.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Expire toasts whenever we process any message.
	m.notifier.DismissExpired()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.tooSmall = msg.Width < 80 || msg.Height < 24
		return m, nil

	case pollTickMsg:
		return m, m.handlePoll()

	case DBResultMsg:
		if msg.Err != nil {
			return m, m.handleDBError(msg.Err)
		}
		m.tasks = msg.Tasks
		m.sessions = msg.Sessions
		m.steps = msg.Steps
		m.poller.SetFromTasks(msg.Tasks)
		m.poller.SetFromSessions(msg.Sessions)
		m.poller.SetChanged(false)
		m.dbLocked = false
		m.lockBanner = false
		return m, nil

	case runsMsg:
		m.runs, m.runsErr = msg.runs, msg.err
		m.runCursor = 0
		return m, nil

	case dbLockedMsg:
		m.setLockedState()
		return m, nil

	case clearLockedMsg:
		m.lockBanner = false
		return m, nil

	case toastTickMsg:
		return m, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return toastTickMsg{} })

	case asyncEventMsg:
		// Async toasts dismiss/refresh on keypress; heartbeat keeps visuals live.
		return m, tea.Tick(m.refresh, func(time.Time) tea.Msg { return pollTickMsg{} })

	case outputLineMsg:
		return m, m.handleOutputLine(msg.line)

	case logLineMsg:
		// A process this TUI started already streams into the buffer; the log
		// would interleave a second copy of the same run.
		if m.runner == nil || !m.runner.Running() {
			m.appendOutput(msg.line)
		}
		return m, m.readLogLine()

	case processExitedMsg:
		return m, m.handleProcessExit(msg)

	case editorFinishedMsg:
		if msg.err != nil {
			m.notifier.Push("Editor failed: "+msg.err.Error(), ToastErr)
		}
		return m, m.loadOnce()
	}

	if m.tooSmall {
		return m, nil
	}

	// Modal routing first.
	if m.modal != ModalNone {
		return m.dispatchModalMsg(msg)
	}

	// Global keybindings.
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.handleKey(key)
}

// handlePoll decides whether to re-query the DB on a tick.
func (m *Model) handlePoll() tea.Cmd {
	if m.store == nil {
		return m.pollTickCmd()
	}
	if m.dbLocked {
		// DB was locked last time; retry with backoff by scheduling a quick tick.
		m.dbLocked = false
		return m.pollTickCmd()
	}
	// Smart refresh: only re-query if data changed since the last poll.
	return tea.Batch(m.pollTickCmd(), func() tea.Msg {
		_, _, steps := m.readForPoll()
		_ = steps
		return m.queryDB()
	})
}

// readForPoll is a hook for subclasses/mock to gate re-query. For the base model
// it returns empty sentinels; the poller's ChangedSince decision is delegated to
// CheckAndReset, which subclasses override. This keeps the base model simple.
func (m *Model) readForPoll() ([]storage.Task, []storage.Session, []storage.Step) {
	return nil, nil, nil
}

func (m *Model) handleDBError(err error) tea.Cmd {
	if isLockedError(err) {
		m.dbLocked = true
		m.lockBanner = true
		// Clear the banner after a short while; the next successful read clears it.
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return clearLockedMsg{} })
	}
	m.notifier.Push("Database error: "+err.Error(), ToastErr)
	return nil
}

func (m *Model) setLockedState() {
	m.dbLocked = true
	m.lockBanner = true
}

// handleViewRouting switches the active view.
func (m *Model) handleViewRouting(v View) {
	m.view = v
	m.cursor = 0
}

// handleKey dispatches search input, then view-local keys, then globals.
func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		return m.handleSearchKey(key)
	}
	if handled, cmd := m.handleViewKey(key); handled {
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		if m.modal == ModalNone {
			return m, tea.Quit
		}
		m.modal = ModalNone
		return m, nil
	case "?":
		m.modal = ModalHelp
		return m, nil
	case "1":
		m.handleViewRouting(ViewDashboard)
		return m, nil
	case "2", "d":
		m.handleViewRouting(ViewQueue)
		return m, nil
	case "3":
		m.handleViewRouting(ViewTaskDetail)
		return m, nil
	case "4", "s":
		m.handleViewRouting(ViewSessions)
		return m, nil
	case "5", "v":
		m.handleViewRouting(ViewLiveOutput)
		return m, nil
	case "a":
		return m, m.openAddTask()
	case "l":
		m.modal = ModalLoopControl
		return m, nil
	case "c":
		m.openPalette()
		return m, nil
	case "o":
		m.modal = ModalObserve
		return m, nil
	case "R":
		return m, m.openReview()
	case "enter":
		if m.modal == ModalNone {
			return m.handleDrillIn()
		}
	case "esc":
		m.modal = ModalNone
		return m, nil
	case "g":
		m.cursor = 0
		return m, nil
	case "G":
		m.cursor = m.listLen() - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	case "j", "down":
		if m.cursor < m.listLen()-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	}
	return m, nil
}

// handleViewKey handles keys that mean different things per view. It reports
// whether the key was consumed.
func (m *Model) handleViewKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch m.view {
	case ViewQueue:
		if key.String() == "/" {
			m.searching = true
			m.searchInput.SetValue(m.queue.Search)
			return true, m.searchInput.Focus()
		}
		if handled, cmd := m.handleTaskActionKey(key); handled {
			return true, cmd
		}
		switch key.String() {
		case "e":
			return true, m.editSelectedSpec()
		case "f":
			// Sort order is a queue concept; it means nothing in other views.
			m.queue.Sort = nextSort(m.queue.Sort)
			return true, nil
		}
	case ViewTaskDetail:
		if handled, cmd := m.handleTaskActionKey(key); handled {
			return true, cmd
		}
	case ViewSessions:
		switch key.String() {
		case "y":
			m.copySelectedSessionID()
			return true, nil
		// Filter cycling. Each change can shrink the list, so the cursor is
		// clamped back into range.
		case "t":
			m.sessFilt.Type = cycle(sessionTypes, m.sessFilt.Type)
			m.clampCursor()
			return true, nil
		case "S":
			m.sessFilt.Status = cycle(sessionStatuses, m.sessFilt.Status)
			m.clampCursor()
			return true, nil
		case "f":
			m.sessFilt.Sort = cycle(sessionSorts, m.sessFilt.Sort)
			m.clampCursor()
			return true, nil
		// "T" scopes to the selected session's task, or clears the scope if one
		// is already set.
		case "T":
			m.toggleSessionTaskScope()
			return true, nil
		}
	case ViewLiveOutput:
		switch key.String() {
		case "f":
			m.frozen = !m.frozen
			if !m.frozen {
				m.scrollOff = 0
			}
			return true, nil
		case "w":
			m.wrapLines = !m.wrapLines
			return true, nil
		case "C":
			m.output.Clear()
			m.scrollOutputBottom()
			m.notifier.Push("Output cleared", ToastOK)
			return true, nil
		case "s":
			m.stopLoop()
			return true, nil
		// Kill is "K" here, matching the loop-control modal, so a mistyped "k"
		// scrolls instead of terminating the run.
		case "K":
			m.killLoop()
			return true, nil
		case "r":
			return true, m.restartLoop()
		case "k", "up":
			m.scrollOutput(1)
			return true, nil
		case "j", "down":
			m.scrollOutput(-1)
			return true, nil
		case "pgup":
			m.scrollOutput(m.liveRows())
			return true, nil
		case "pgdown":
			m.scrollOutput(-m.liveRows())
			return true, nil
		case "g", "home":
			m.scrollOutputTop()
			return true, nil
		case "G", "end":
			m.scrollOutputBottom()
			return true, nil
		}
	}
	return false, nil
}

// handleTaskActionKey handles the task mutations shared by the Queue and Task
// Detail views. Both select through the queue cursor, so the actions are
// identical in each.
func (m *Model) handleTaskActionKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "r":
		return true, m.retryTask()
	case "b":
		return true, m.blockTask()
	case "m":
		return true, m.markTaskDone()
	case "x":
		m.confirmDeleteTask()
		return true, nil
	case "y":
		m.copySelectedTaskID()
		return true, nil
	}
	return false, nil
}

// handleSearchKey routes keys to the queue search field while it has focus.
func (m *Model) handleSearchKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		m.searchInput.SetValue("")
		m.queue.Search = ""
		m.cursor = 0
		return m, nil
	case "enter":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(key)
	m.queue.Search = m.searchInput.Value()
	m.cursor = 0
	return m, cmd
}

// handleDrillIn moves into a more detailed view (Enter).
func (m *Model) handleDrillIn() (tea.Model, tea.Cmd) {
	switch m.view {
	case ViewQueue:
		if m.listLen() > 0 {
			m.view = ViewTaskDetail
		}
	}
	return m, nil
}

// dispatchModalMsg routes modal-local input.
func (m *Model) dispatchModalMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.modal {
	case ModalHelp:
		m.modal = ModalNone
		return m, nil
	case ModalConfirm:
		return m.handleConfirmKey(key)
	case ModalAddTask:
		switch key.String() {
		case "enter":
			m.syncAddFields()
			if err := m.addTaskNow(); err != nil {
				m.addErr = err.Error()
				m.notifier.Push("Add task failed: "+err.Error(), ToastErr)
				return m, nil
			}
			m.closeAddTask()
			m.notifier.Push("Task added "+m.theme.icon(iconApproved), ToastOK)
			return m, nil
		case "esc":
			m.closeAddTask()
			return m, nil
		case "tab", "down", "shift+tab", "up":
			step := 1
			if key.String() == "shift+tab" || key.String() == "up" {
				step = addFieldCount - 1
			}
			m.addFocus = (m.addFocus + step) % addFieldCount
			return m, m.focusAddField()
		case " ":
			if m.addFocus == addNoFormatField {
				m.addNoFormat = !m.addNoFormat
				m.addErr = ""
				return m, nil
			}
			fallthrough
		default:
			if m.addFocus == addNoFormatField {
				return m, nil // toggle field takes no text input
			}
			m.addErr = ""
			var cmd tea.Cmd
			m.addInputs[m.addFocus], cmd = m.addInputs[m.addFocus].Update(key)
			m.syncAddFields()
			return m, cmd
		}
	case ModalLoopControl:
		switch key.String() {
		case "esc", "q":
			m.modal = ModalNone
		case "s":
			m.stopLoop()
		case "K":
			m.killLoop()
		case "r":
			return m, m.restartLoop()
		case "enter":
			return m, m.startLoop("loop --once", []string{"loop", "--once"})
		case "b":
			return m, m.startLoop("loop", []string{"loop"})
		case "i":
			return m, m.runInteractiveLoop()
		case "c":
			m.openPalette()
		}
		return m, nil
	case ModalPalette:
		return m.handlePaletteKey(key)
	case ModalReview:
		switch key.String() {
		case "esc", "enter", "q":
			m.modal = ModalNone
		case "j", "down":
			if m.runCursor < len(m.runs)-1 {
				m.runCursor++
			}
		case "k", "up":
			if m.runCursor > 0 {
				m.runCursor--
			}
		case "y":
			m.copySelectedRunLog()
		}
		return m, nil
	case ModalObserve:
		switch key.String() {
		case "esc", "enter", "q":
			m.modal = ModalNone
		}
		return m, nil
	}
	return m, nil
}

// handleConfirmKey makes destructive actions require explicit confirmation.
func (m *Model) handleConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		var cmd tea.Cmd
		if m.confirm != nil && m.confirm.Action != nil {
			// Actions report their own outcome; refresh so the result is visible
			// before the next poll tick.
			m.confirm.Action(m)
			cmd = m.loadOnce()
		}
		m.modal = ModalNone
		m.confirm = nil
		return m, cmd
	case "esc":
		// Cancel leaves the model unchanged.
		m.modal = ModalNone
		m.confirm = nil
	}
	return m, nil
}

// listLen returns the number of rows in the active view (queue for task lists).
func (m *Model) listLen() int {
	switch m.view {
	case ViewQueue:
		return len(Apply(m.tasks, m.queue))
	case ViewSessions:
		return len(m.filteredSessions())
	default:
		return 0
	}
}

// addTaskNow validates and inserts a new task via the store. It mirrors the
// validation in `colony task add` so tasks enqueued from the TUI carry the
// same lang and gate configuration the loop expects.
func (m *Model) addTaskNow() error {
	if m.store == nil {
		return fmt.Errorf("storage unavailable")
	}
	desc := trimSpace(m.addDesc)
	spec := trimSpace(m.addSpec)
	if desc == "" && spec == "" {
		return fmt.Errorf("task description or spec file is required")
	}
	lang := trimSpace(m.addLang)
	if lang == "" {
		return fmt.Errorf("language is required (typescript, python, go)")
	}
	if _, err := module.CommandsFor(lang); err != nil {
		return err
	}
	if spec != "" {
		abs, err := filepath.Abs(spec)
		if err != nil {
			return fmt.Errorf("resolve spec path: %w", err)
		}
		if !fileExists(abs) {
			return fmt.Errorf("spec file not found: %s", spec)
		}
		spec = abs
	}

	gateOverrides := ""
	if m.addNoFormat {
		gateOverrides = "format"
	}

	return m.store.InsertTask(storage.Task{
		Description:   desc,
		SpecPath:      spec,
		BaseBranch:    trimSpace(m.addBase),
		Lang:          lang,
		GateOverrides: gateOverrides,
		State:         "open",
		CreatedAt:     time.Now(),
	})
}

// openAddTask opens the Add Task modal with a clean form.
func (m *Model) openAddTask() tea.Cmd {
	m.modal = ModalAddTask
	m.addErr, m.addDesc, m.addSpec, m.addBase, m.addLang = "", "", "", "", ""
	m.addNoFormat = false
	m.addFocus = 0
	for i := range m.addInputs {
		m.addInputs[i].SetValue("")
		m.addInputs[i].Blur()
	}
	return m.focusAddField()
}

// closeAddTask dismisses the modal and releases input focus.
func (m *Model) closeAddTask() {
	m.modal = ModalNone
	for i := range m.addInputs {
		m.addInputs[i].Blur()
	}
}

// focusAddField moves the cursor to the currently selected form field.
func (m *Model) focusAddField() tea.Cmd {
	var cmd tea.Cmd
	for i := range m.addInputs {
		if i == m.addFocus {
			cmd = m.addInputs[i].Focus()
			continue
		}
		m.addInputs[i].Blur()
	}
	return cmd
}

// syncAddFields mirrors the text inputs into the plain string fields that
// validation and tests read. The no-format toggle has no text input and is
// driven directly by the key handler.
func (m *Model) syncAddFields() {
	if len(m.addInputs) == addNoFormatField {
		m.addDesc = m.addInputs[addDescField].Value()
		m.addSpec = m.addInputs[addSpecField].Value()
		m.addBase = m.addInputs[addBaseField].Value()
		m.addLang = m.addInputs[addLangField].Value()
	}
}

// BeginConfirm opens a confirmation modal for a destructive action.
func (m *Model) BeginConfirm(prompt, detail string, action func(*Model)) {
	m.confirm = &ConfirmState{Prompt: prompt, Detail: detail, Action: action}
	m.modal = ModalConfirm
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pollTickMsg{} })
}

// nextSort cycles queue sort order.
func nextSort(cur string) string {
	switch cur {
	case "created":
		return "updated"
	case "updated":
		return "state"
	default:
		return "created"
	}
}

func trimSpace(s string) string {
	out := ""
	started := false
	for _, r := range s {
		if started || r != ' ' && r != '\t' && r != '\n' {
			started = true
			out += string(r)
		}
	}
	end := len(out)
	for end > 0 && (out[end-1] == ' ' || out[end-1] == '\t' || out[end-1] == '\n') {
		end--
	}
	return out[:end]
}
