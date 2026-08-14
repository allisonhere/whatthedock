package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenVersion screen = iota
	screenConfirm
	screenRunning
	screenDone
)

// stepStatus is one release step's current state, shown as a checklist row
// in screenRunning/screenDone.
type stepStatus struct {
	label  string
	done   bool
	err    error
	output string
}

type model struct {
	width, height int

	screen screen

	// repo state gathered at startup, shown on screenVersion.
	branch    string
	dirty     bool
	latestTag string
	commits   []string // commits since latestTag (or recent HEAD commits if no tag yet)
	diffStat  string

	versionInput string
	dryRun       bool

	steps   []stepStatus
	current int
	log     []string
	events  chan stepEvent
	failed  bool

	quitting bool
}

// stepEvent is sent from the goroutine running release steps (started by
// runReleaseCmd) to the UI, one per step transition. Draining a channel on
// a tick, rather than one message per event via tea.Cmd chaining, matches
// the pattern WhatTheDock's own log/progress streaming already uses.
type stepEvent struct {
	index    int
	label    string
	starting bool
	output   string
	err      error
	done     bool // all steps finished (index/label/etc. are zero)
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func initialModel(branch string, dirty bool, latestTag, suggestedVersion string, commits []string, diffStat string, dryRun bool) model {
	return model{
		screen:       screenVersion,
		branch:       branch,
		dirty:        dirty,
		latestTag:    latestTag,
		commits:      commits,
		diffStat:     diffStat,
		versionInput: suggestedVersion,
		dryRun:       dryRun,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		if m.events == nil {
			return m, nil
		}
		m.drainEvents()
		if m.screen == screenRunning {
			return m, tick()
		}
		return m, nil
	}
	return m, nil
}

func (m *model) drainEvents() {
	for {
		select {
		case ev, ok := <-m.events:
			if !ok {
				m.events = nil
				return
			}
			m.applyEvent(ev)
		default:
			return
		}
	}
}

func (m *model) applyEvent(ev stepEvent) {
	if ev.done {
		m.screen = screenDone
		return
	}
	if ev.starting {
		m.current = ev.index
		m.log = append(m.log, "→ "+ev.label)
		return
	}
	m.steps[ev.index].done = true
	m.steps[ev.index].err = ev.err
	m.steps[ev.index].output = ev.output
	if ev.err != nil {
		m.failed = true
		m.log = append(m.log, "✗ "+ev.label+": "+ev.err.Error())
	} else {
		m.log = append(m.log, "✓ "+ev.label)
	}
	if ev.output != "" {
		m.log = append(m.log, indentLines(ev.output)...)
	}
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenVersion:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.versionInput == "" {
				return m, nil
			}
			m.steps = releasePlanSteps(m.versionInput)
			m.screen = screenConfirm
			return m, nil
		case "backspace":
			if len(m.versionInput) > 0 {
				m.versionInput = m.versionInput[:len(m.versionInput)-1]
			}
			return m, nil
		default:
			if len(msg.Runes) > 0 {
				m.versionInput += string(msg.Runes)
			}
			return m, nil
		}

	case screenConfirm:
		switch msg.String() {
		case "y", "enter":
			m.events = make(chan stepEvent, 32)
			m.screen = screenRunning
			return m, tea.Batch(runReleaseCmd(m.versionInput, m.dryRun, m.events), tick())
		case "n", "esc", "ctrl+c":
			m.screen = screenVersion
			return m, nil
		}
		return m, nil

	case screenRunning:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case screenDone:
		switch msg.String() {
		case "q", "esc", "enter", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

func indentLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if i > start {
				out = append(out, "    "+s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
