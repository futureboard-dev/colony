package tui

import (
	"os/exec"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// startLoop spawns `colony loop [args...]`, streaming its output into the Live
// Output view. Callers pass the flags; this only owns process lifecycle.
func (m *Model) startLoop(label string, args []string) tea.Cmd {
	if m.runner == nil {
		m.notifier.Push("Runner unavailable", ToastErr)
		return nil
	}
	if m.runner.Running() {
		m.notifier.Push(m.runner.Label()+" is already running", ToastErr)
		return nil
	}
	if state, _ := m.loopState(); state == "running" {
		m.notifier.Push("A loop is already running (loop.pid)", ToastErr)
		return nil
	}

	// A stale sentinel from a previous stop would halt the new run immediately.
	ClearStopSentinel(m.opts.ColonyDir)

	dir := or(m.opts.Root, ".")
	if err := m.runner.Start(colonyBinary(), dir, label, args); err != nil {
		m.notifier.Push("Start failed: "+err.Error(), ToastErr)
		return nil
	}

	m.output.Push(m.theme.icon("▶") + " colony " + strings.Join(args, " "))
	m.view = ViewLiveOutput
	m.modal = ModalNone
	m.notifier.Push("Started "+label, ToastOK)
	return tea.Batch(m.readOutput(), m.awaitExit(label))
}

// readOutput pulls one buffered line and re-arms itself, which is how a
// blocking channel is drained inside the Bubble Tea update loop.
func (m *Model) readOutput() tea.Cmd {
	lines := m.runner.Lines()
	return func() tea.Msg {
		line, ok := <-lines
		if !ok {
			return nil
		}
		return outputLineMsg{line: line}
	}
}

// awaitExit reports the child's exit status back into the update loop.
func (m *Model) awaitExit(label string) tea.Cmd {
	runner := m.runner
	return func() tea.Msg {
		return processExitedMsg{label: label, err: runner.Wait()}
	}
}

// stopLoop writes the sentinel so the loop finishes its current task and exits.
func (m *Model) stopLoop() {
	if err := WriteStopSentinel(m.opts.ColonyDir); err != nil {
		m.notifier.Push("Stop failed: "+err.Error(), ToastErr)
		return
	}
	m.output.Push(m.theme.icon("◷") + " stop requested — will exit after the current task")
	m.notifier.Push("Stop signal sent", ToastOK)
}

// killLoop terminates the loop now, preferring the process this TUI started and
// falling back to the PID file for loops started elsewhere.
func (m *Model) killLoop() {
	if m.runner != nil && m.runner.Running() {
		if err := m.runner.Signal(syscall.SIGTERM); err != nil {
			m.notifier.Push("Kill failed: "+err.Error(), ToastErr)
			return
		}
		m.output.Push(m.theme.icon("✗") + " SIGTERM sent")
		m.notifier.Push("SIGTERM sent", ToastOK)
		return
	}
	if err := KillLoop(m.opts.ColonyDir); err != nil {
		m.notifier.Push("Kill failed: "+err.Error(), ToastErr)
		return
	}
	m.notifier.Push("SIGTERM sent to loop", ToastOK)
}

// restartLoop kills any running loop and starts a fresh continuous run.
func (m *Model) restartLoop() tea.Cmd {
	if m.runner != nil && m.runner.Running() {
		_ = m.runner.Signal(syscall.SIGTERM)
		_ = m.runner.Wait()
	} else if state, _ := m.loopState(); state == "running" {
		if err := KillLoop(m.opts.ColonyDir); err != nil {
			m.notifier.Push("Restart failed: "+err.Error(), ToastErr)
			return nil
		}
	}
	return m.startLoop("loop", []string{"loop"})
}

// runInteractiveLoop suspends the TUI and hands the terminal to a single
// foreground pass, so the agent session can be watched and steered.
func (m *Model) runInteractiveLoop() tea.Cmd {
	if m.runner != nil && m.runner.Running() {
		m.notifier.Push(m.runner.Label()+" is already running", ToastErr)
		return nil
	}
	ClearStopSentinel(m.opts.ColonyDir)
	m.modal = ModalNone

	cmd := exec.Command(colonyBinary(), "loop", "--once")
	cmd.Dir = or(m.opts.Root, ".")
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return processExitedMsg{label: "loop --once (interactive)", err: err}
	})
}

// runPaletteCommand builds argv from the palette form and runs it.
func (m *Model) runPaletteCommand() tea.Cmd {
	spec := paletteCommands[m.palette.spec]
	argv, err := m.palette.buildArgv(spec)
	if err != nil {
		m.palette.err = err.Error()
		return nil
	}
	m.palette.err = ""
	return m.startLoop(spec.Name, argv)
}

// handleOutputLine appends a streamed line unless the view is frozen.
func (m *Model) handleOutputLine(line string) tea.Cmd {
	if !m.frozen {
		m.output.Push(line)
	}
	return m.readOutput()
}

// handleProcessExit reports the child's outcome and refreshes the queue, since
// a finished run almost always changed task state.
func (m *Model) handleProcessExit(msg processExitedMsg) tea.Cmd {
	if msg.err != nil {
		m.output.Push(m.theme.icon("✗") + " " + msg.label + " exited: " + msg.err.Error())
		m.notifier.Push(msg.label+" failed: "+msg.err.Error(), ToastErr)
	} else {
		m.output.Push(m.theme.icon("✓") + " " + msg.label + " finished")
		m.notifier.Push(msg.label+" finished", ToastOK)
	}
	return m.loadOnce()
}
