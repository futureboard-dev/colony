package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/fix"
	"github.com/futureboard-dev/colony/pkg/module"
)

// fix.* package functions as swappable vars, the same way pkg/fix itself
// swaps onPath/loadConfig/launchInteractive for tests: production wiring
// calls the real fix package, and tests substitute deterministic behavior
// instead of depending on which agent CLIs happen to be on the test
// machine's PATH or touching the real task database.
var (
	fixInstalledTools         = fix.InstalledTools
	fixResolveChoice          = fix.ResolveChoice
	fixResolveChoiceExcluding = fix.ResolveChoiceExcluding
	fixOpenSession            = fix.OpenSession
	fixRetry                  = fix.Retry
	fixDiagnose               = fix.Diagnose
	fixExplain                = fix.Explain
	fixIsInvalidModel         = fix.IsInvalidModel
)

// blockFlowStage is the step of the "When a task is blocked" / "After a fix
// session" conversation currently on screen.
type blockFlowStage int

const (
	blockStageDiagnosing blockFlowStage = iota // one automatic triage attempt, Phase 4
	blockStageAction                           // [f]ix [t]ell [o]pen shell [s]kip
	blockStagePicker                           // shared tool/model picker, before "fix"
	blockStageHint                             // one-line hint for "tell it what to do"
	blockStageResume                           // "Resume task <id>?" after fix/shell
)

// blockFlowState carries one blocked task through the recovery conversation.
// A nil Model.blockFlow means ModalBlockFlow is not showing.
type blockFlowState struct {
	taskID      string
	description string
	reason      string // the block reason, i.e. the task's LastFeedback
	cause       string // fix.Explain's cause (falls back to reason)
	nextStep    string
	tool        string // task.BlockTool — the tool that got stuck, preselected

	stage    blockFlowStage
	picker   flowPicker
	hint     textinput.Model
	err      string
	worktree string
}

// diagnoseMsg carries the result of one automatic-triage attempt.
type diagnoseMsg struct {
	taskID string
	diag   fix.Diagnosis
	err    error
}

// sessionFinishedMsg reports the outcome of a suspended fix/shell session.
type sessionFinishedMsg struct {
	kind string // "fix" | "shell"
	err  error
}

// openBlockFlow opens the blocked-task recovery prompt for the selected task.
func (m *Model) openBlockFlow() tea.Cmd {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return nil
	}

	bf := &blockFlowState{
		taskID:      sel.ID,
		description: sel.Description,
		reason:      sel.LastFeedback,
		cause:       sel.LastFeedback,
		tool:        sel.BlockTool,
		worktree:    m.taskWorktreePath(sel),
	}
	if expl, err := fixExplain(context.Background(), fix.Request{
		TaskID: sel.ID, Reason: sel.LastFeedback, Description: sel.Description,
	}); err == nil {
		bf.cause, bf.nextStep = expl.Cause, expl.NextStep
	}
	m.blockFlow = bf
	m.modal = ModalBlockFlow

	if sel.BlockAttempts > 0 {
		bf.stage = blockStageAction
		return nil
	}
	bf.stage = blockStageDiagnosing
	return m.runDiagnosis(sel)
}

// taskWorktreePath best-effort resolves the task's worktree directory from
// its branch. The caller (fix.OpenSession, or the shell command) tolerates a
// path that doesn't exist, falling back to the project root.
func (m *Model) taskWorktreePath(sel storageTaskRef) string {
	if sel.Branch == "" {
		return ""
	}
	root := or(m.opts.Root, ".")
	return module.WorktreePath(module.ProjectName(root), sel.Branch)
}

// runDiagnosis fires fix.Diagnose for one automatic-triage attempt (Phase 4).
// Every default Triager returns fix.ErrNotConfigured until 05 registers one,
// which handleDiagnoseResult treats the same as "nothing to fix automatically".
func (m *Model) runDiagnosis(sel storageTaskRef) tea.Cmd {
	req := fix.Request{
		TaskID: sel.ID, Description: sel.Description, Reason: sel.LastFeedback,
		Worktree: m.taskWorktreePath(sel), Flow: "fix",
	}
	return func() tea.Msg {
		diag, err := fixDiagnose(context.Background(), req)
		return diagnoseMsg{taskID: sel.ID, diag: diag, err: err}
	}
}

// handleDiagnoseResult reacts to a finished triage attempt. A stale result
// (the modal closed, or moved past the diagnosing stage) is dropped.
func (m *Model) handleDiagnoseResult(msg diagnoseMsg) tea.Cmd {
	if m.blockFlow == nil || m.blockFlow.taskID != msg.taskID || m.blockFlow.stage != blockStageDiagnosing {
		return nil
	}
	if m.store != nil {
		_, _ = m.store.IncrementBlockAttempts(msg.taskID)
	}
	if msg.err != nil || !msg.diag.Fixed {
		if msg.err == nil && msg.diag.Explanation.Cause != "" {
			m.blockFlow.cause, m.blockFlow.nextStep = msg.diag.Explanation.Cause, msg.diag.Explanation.NextStep
		}
		m.blockFlow.stage = blockStageAction
		return nil
	}

	taskID, reason := msg.taskID, m.blockFlow.reason
	m.modal, m.blockFlow = ModalNone, nil
	m.notifier.Push("Auto-fixed: "+msg.diag.Change, ToastOK)
	return m.performRetry(taskID, reason, "")
}

// handleBlockFlowKey routes one key through the active stage.
func (m *Model) handleBlockFlowKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	bf := m.blockFlow
	if bf == nil {
		m.modal = ModalNone
		return m, nil
	}
	switch bf.stage {
	case blockStageDiagnosing:
		return m, nil // no input while the one triage attempt is in flight
	case blockStageAction:
		return m.handleActionStageKey(key)
	case blockStagePicker:
		return m.handlePickerStageKey(key)
	case blockStageHint:
		return m.handleHintStageKey(key)
	case blockStageResume:
		return m.handleResumeStageKey(key)
	}
	return m, nil
}

// handleActionStageKey implements the "[f] Fix with agent  [t] Tell it what
// to do  [o] Open shell  [s] Skip" menu. Enter accepts the default action,
// Fix with agent.
func (m *Model) handleActionStageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "f", "enter":
		return m, m.startFixPicker()
	case "t":
		bf := m.blockFlow
		bf.stage = blockStageHint
		bf.hint = textinput.New()
		bf.hint.Placeholder = "what should it try?"
		bf.hint.CharLimit = 300
		bf.err = ""
		return m, bf.hint.Focus()
	case "o":
		return m, m.openTaskShell()
	case "s":
		m.skipBlockedTask()
		return m, nil
	case "esc":
		m.closeBlockFlow()
		return m, nil
	}
	return m, nil
}

// startFixPicker looks up the installed tools and shows the shared picker,
// preselected per fix.ResolveChoice (the task's stuck tool wins when it's
// still installed).
func (m *Model) startFixPicker() tea.Cmd {
	bf := m.blockFlow
	req := fix.Request{
		TaskID: bf.taskID, Description: bf.description, Reason: bf.reason,
		Worktree: bf.worktree, Flow: "fix", Choice: fix.Choice{Tool: fix.Tool(bf.tool)},
	}
	return func() tea.Msg {
		ctx := context.Background()
		tools, err := fixInstalledTools(ctx)
		if err != nil || len(tools) == 0 {
			return installedToolsMsg{taskID: bf.taskID, err: err}
		}
		choice, err := fixResolveChoice(ctx, req)
		if err != nil {
			return installedToolsMsg{taskID: bf.taskID, tools: tools, err: err}
		}
		return installedToolsMsg{taskID: bf.taskID, tools: tools, choice: choice}
	}
}

// installedToolsMsg carries the result of resolving tools/choice for the
// picker stage.
type installedToolsMsg struct {
	taskID string
	tools  []fix.Tool
	choice fix.Choice
	err    error
}

// handleInstalledTools reacts to startFixPicker's result: either the shared
// picker is shown with a preselected choice, or the flow is blocked and the
// action menu explains how to install a tool (per "None: Blocks the flow and
// shows how to install one").
func (m *Model) handleInstalledTools(msg installedToolsMsg) tea.Cmd {
	if m.blockFlow == nil || m.blockFlow.taskID != msg.taskID {
		return nil
	}
	bf := m.blockFlow
	if msg.err != nil || len(msg.tools) == 0 {
		bf.stage = blockStageAction
		if msg.err != nil {
			bf.err = msg.err.Error()
		} else {
			bf.err = "no agent CLI found — install claude, codex, or crush"
		}
		return nil
	}
	bf.picker = newFlowPicker(msg.tools, msg.choice)
	bf.stage = blockStagePicker
	bf.err = ""
	return nil
}

// handlePickerStageKey routes a key into the shared flowPicker.
func (m *Model) handlePickerStageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	bf := m.blockFlow
	result, cmd := bf.picker.handleKey(key)
	switch result {
	case pickerConfirmed:
		return m, m.launchFixSession()
	case pickerCancelled:
		m.closeBlockFlow()
		return m, nil
	}
	return m, cmd
}

// launchFixSession suspends the TUI and opens a fresh agent session already
// loaded with the task's error, logs, and worktree, mirroring how
// runInteractiveLoop suspends for `colony loop --once`. There is no
// subprocess to hand to tea.ExecProcess here — fix.OpenSession runs the
// agent in-process via pkg/llm — so this uses tea.Exec with a small
// ExecCommand adapter instead.
func (m *Model) launchFixSession() tea.Cmd {
	bf := m.blockFlow
	choice := bf.picker.choice()
	req := fix.Request{
		TaskID: bf.taskID, Description: bf.description, Reason: bf.reason,
		Worktree: bf.worktree, Flow: "fix", Choice: choice,
	}
	if m.store != nil {
		_ = m.store.UpdateTaskBlockTool(bf.taskID, string(choice.Tool))
	}
	m.modal = ModalNone
	return tea.Exec(funcExecCommand{fn: func() error {
		return fixOpenSession(context.Background(), req)
	}}, func(err error) tea.Msg {
		return sessionFinishedMsg{kind: "fix", err: err}
	})
}

// funcExecCommand adapts a plain function to tea.ExecCommand so an in-process
// call (rather than a child process) can suspend the TUI's terminal the same
// way tea.ExecProcess does for a subprocess.
type funcExecCommand struct{ fn func() error }

func (f funcExecCommand) Run() error          { return f.fn() }
func (f funcExecCommand) SetStdin(io.Reader)  {}
func (f funcExecCommand) SetStdout(io.Writer) {}
func (f funcExecCommand) SetStderr(io.Writer) {}

// openTaskShell suspends the TUI and hands the terminal to a shell in the
// task's worktree.
func (m *Model) openTaskShell() tea.Cmd {
	bf := m.blockFlow
	shell := or(os.Getenv("SHELL"), "/bin/sh")
	dir := bf.worktree
	if dir == "" || !dirExists(dir) {
		dir = or(m.opts.Root, ".")
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	m.modal = ModalNone
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return sessionFinishedMsg{kind: "shell", err: err}
	})
}

// dirExists reports whether path refers to an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// handleHintStageKey drives "Tell it what to do": a one-line hint that
// retries the task immediately (no agent session, no resume prompt).
func (m *Model) handleHintStageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	bf := m.blockFlow
	switch key.String() {
	case "esc":
		bf.hint.Blur()
		bf.stage = blockStageAction
		return m, nil
	case "enter":
		hint := bf.hint.Value()
		bf.hint.Blur()
		taskID, reason := bf.taskID, bf.reason
		m.closeBlockFlow()
		return m, m.performRetry(taskID, reason, hint)
	}
	var cmd tea.Cmd
	bf.hint, cmd = bf.hint.Update(key)
	return m, cmd
}

// handleResumeStageKey drives "Resume task <id>?" after a fix/shell session,
// defaulting to yes.
func (m *Model) handleResumeStageKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	bf := m.blockFlow
	switch key.String() {
	case "enter", "y", "Y":
		taskID, reason := bf.taskID, bf.reason
		m.closeBlockFlow()
		return m, m.performRetry(taskID, reason, "")
	case "n", "N", "esc":
		m.notifier.Push(bf.taskID+" stays blocked", ToastOK)
		m.closeBlockFlow()
		return m, nil
	}
	return m, nil
}

// performRetry resumes a blocked task via fix.Retry, which keeps the block
// reason (falling back to the task's stored feedback when reason is empty)
// plus any hint, and keeps the task in its existing worktree.
func (m *Model) performRetry(taskID, reason, hint string) tea.Cmd {
	req := fix.Request{TaskID: taskID, Reason: reason, Hint: hint, Flow: "fix"}
	err := fixRetry(context.Background(), req)
	if err != nil {
		m.notifier.Push("Resume failed: "+err.Error(), ToastErr)
		return nil
	}
	m.notifier.Push(taskID+" → needs-fix", ToastOK)
	return m.loadOnce()
}

// skipBlockedTask leaves the task blocked and closes the prompt — "park and
// move on" so the loop keeps running the rest of the queue.
func (m *Model) skipBlockedTask() {
	m.notifier.Push(m.blockFlow.taskID+" skipped — still blocked", ToastOK)
	m.closeBlockFlow()
}

// closeBlockFlow dismisses the modal and clears its state.
func (m *Model) closeBlockFlow() {
	m.modal = ModalNone
	m.blockFlow = nil
}

// handleSessionFinished reacts to a suspended fix/shell session returning
// control to the TUI. An invalid-model failure returns to the picker instead
// of ever marking the task newly blocked.
func (m *Model) handleSessionFinished(msg sessionFinishedMsg) tea.Cmd {
	bf := m.blockFlow
	if bf == nil {
		return m.loadOnce()
	}
	if msg.kind == "fix" && msg.err != nil && fixIsInvalidModel(msg.err) {
		bad := bf.picker.model
		req := fix.Request{
			TaskID: bf.taskID, Reason: bf.reason, Worktree: bf.worktree, Flow: "fix",
			Choice: fix.Choice{Tool: bf.picker.choice().Tool},
		}
		choice, resolveErr := fixResolveChoiceExcluding(context.Background(), req, bad)
		tools := bf.picker.tools
		m.modal = ModalBlockFlow
		bf.stage = blockStagePicker
		bf.picker = newFlowPicker(tools, choice)
		if resolveErr != nil {
			bf.picker.err = resolveErr.Error()
		} else {
			bf.picker.err = msg.err.Error()
		}
		return nil
	}
	if msg.err != nil {
		m.notifier.Push(msg.kind+" session failed: "+msg.err.Error(), ToastErr)
	}
	m.modal = ModalBlockFlow
	bf.stage = blockStageResume
	return m.loadOnce()
}

// renderBlockFlowModal renders the active stage of the blocked-task
// recovery conversation: the action menu, the shared picker, the hint
// input, or the resume confirmation — with the resolved "tool · model"
// line always visible once a tool has been chosen.
func (m *Model) renderBlockFlowModal(frameW, frameH int) string {
	w := modalWidth(frameW, 64)
	bf := m.blockFlow
	if bf == nil {
		return m.theme.modalBox("Blocked", " (nothing to show)\n", w)
	}

	var b strings.Builder
	fmt.Fprintf(&b, " %s %s\n", m.theme.Bold.Render("Task "+bf.taskID+" blocked:"),
		truncate(bf.cause, w-20))
	if bf.nextStep != "" {
		fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Next:"), truncate(bf.nextStep, w-10))
	}
	if label := bf.picker.label(); label != "" {
		fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Using:"), m.theme.Accent.Render(label))
	}
	b.WriteString("\n")

	switch bf.stage {
	case blockStageDiagnosing:
		b.WriteString(" " + m.theme.Dim.Render(m.theme.icon(iconProgress)+" checking for a quick fix…") + "\n")
	case blockStageAction:
		if bf.err != "" {
			b.WriteString(" " + m.theme.Error.Render(m.theme.icon(iconCrashed)+" "+bf.err) + "\n\n")
		}
		b.WriteString(m.styleHints("[f] Fix with agent   [t] Tell it what to do") + "\n")
		b.WriteString(m.styleHints("[o] Open shell        [s] Skip") + "\n\n")
		b.WriteString(m.styleHints("[Enter] fix (default)   [Esc] cancel"))
	case blockStagePicker:
		b.WriteString(bf.picker.view(m.theme))
		b.WriteString("\n" + m.styleHints("[Enter] start session   [Esc] cancel"))
	case blockStageHint:
		b.WriteString(" " + m.theme.Bold.Render("Tell it what to do:") + "\n")
		b.WriteString(" " + m.theme.FieldFocus.Render(" "+bf.hint.View()+" ") + "\n\n")
		b.WriteString(m.styleHints("[Enter] retry with hint   [Esc] cancel"))
	case blockStageResume:
		fmt.Fprintf(&b, " %s\n\n", m.theme.Bold.Render("Resume task "+bf.taskID+"?"))
		b.WriteString(m.styleHints("[Enter] yes (default)   [n] no"))
	}

	return m.theme.modalBox("Task Blocked", b.String(), w)
}
