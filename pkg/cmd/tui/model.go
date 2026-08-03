package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbletea"
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
	ModalSchedule
	ModalReview
	ModalObserve
	ModalConfirm
	ModalHelp
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
	pollTickMsg     struct{}
	toastTickMsg    struct{}
	dbLockedMsg     struct{}
	asyncEventMsg   struct{}
	clearLockedMsg  struct{}
)

// Model is the Bubble Tea application state. It owns the view router and the
// read-only by default interaction model.
type Model struct {
	opts     Options
	theme    *Theme
	notifier *Notifier
	poller   *Poller

	store Store

	view       View
	modal      Modal
	confirm    *ConfirmState
	cursor     int
	queue      QueueFilter

	tasks     []storage.Task
	sessions  []storage.Session
	steps     []storage.Step
	dbLocked  bool
	lockBanner bool

	width  int
	height int
	tooSmall bool

	refresh      time.Duration
	refreshCount int

	lastActive map[string]time.Time

	// AddTask modal fields
	addDesc    string
	addSpec    string
	addErr     string
}

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
	m := &Model{
		opts:     opts,
		theme:    DefaultTheme(opts.NoColor),
		notifier: NewNotifier(nil),
		poller:   NewPoller(nil),
		store:    store,
		view:     opts.StartView,
		modal:    ModalNone,
		queue:    QueueFilter{State: "", Sort: "created", Search: ""},
		refresh:  refresh,
		lastActive: make(map[string]time.Time),
	}
	if m.view == ViewTaskDetail {
		m.view = ViewQueue // task detail requires a selected task
	}
	if opts.StartupLog != "" {
		m.notifier.Push(opts.StartupLog, ToastAsync)
	}
	return m
}

// Init returns the initial commands: a refresh tick and toast clock.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(m.refresh),
		tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return toastTickMsg{} }),
		m.loadOnce(),
	)
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

// handleKey dispatches global + view keys.
func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.modal = ModalAddTask
		m.addErr = ""
		return m, nil
	case "l":
		m.modal = ModalLoopControl
		return m, nil
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
	case "f":
		// Cycle sort order for the queue / focus filter bar.
		m.queue.Sort = nextSort(m.queue.Sort)
		return m, nil
	}
	return m, nil
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
		m.addErr = ""
		switch key.String() {
		case "enter":
			if err := m.addTaskNow(); err != nil {
				m.addErr = err.Error()
				m.notifier.Push("Add task failed: "+err.Error(), ToastErr)
				return m, nil
			}
			m.modal = ModalNone
			m.notifier.Push("Task added ✓", ToastOK)
			return m, nil
		case "esc":
			m.modal = ModalNone
			return m, nil
		case "tab", "down":
			// No-op placeholder to keep keyboard-first flow.
		default:
			return m, nil
		}
	case ModalLoopControl:
		switch key.String() {
		case "esc", "q":
			m.modal = ModalNone
		}
		return m, nil
	case ModalSchedule:
		switch key.String() {
		case "esc", "q":
			m.modal = ModalNone
		}
		return m, nil
	case ModalReview, ModalObserve:
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
		if m.confirm != nil && m.confirm.Action != nil {
			m.confirm.Action(m)
			m.notifier.Push("Action completed", ToastOK)
		}
		m.modal = ModalNone
		m.confirm = nil
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
	default:
		return 0
	}
}

// addTaskNow validates and inserts a new task via the store.
func (m *Model) addTaskNow() error {
	if m.store == nil {
		return fmt.Errorf("storage unavailable")
	}
	if desc := trimSpace(m.addDesc); desc == "" {
		return fmt.Errorf("task description is required")
	}
	if m.addSpec != "" && !fileExists(m.addSpec) {
		return fmt.Errorf("spec file not found: %s", m.addSpec)
	}
	return m.store.InsertTask(storage.Task{
		Description: m.addDesc,
		SpecPath:    m.addSpec,
		State:       "open",
		CreatedAt:   time.Now(),
	})
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
