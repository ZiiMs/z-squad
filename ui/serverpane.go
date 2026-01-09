package ui

import (
	"claude-squad/session"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var serverPaneStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

type ServerPane struct {
	width        int
	height       int
	text         string
	viewport     viewport.Model
	isScrolling  bool
	userScrolled bool // Track if user manually scrolled
}

func NewServerPane() *ServerPane {
	return &ServerPane{
		viewport: viewport.New(0, 0),
	}
}

func (s *ServerPane) SetSize(width, maxHeight int) {
	s.width = width
	s.height = maxHeight
	s.viewport.Width = width
	s.viewport.Height = maxHeight
}

func (s *ServerPane) UpdateContent(instance *session.Instance) error {
	switch {
	case instance == nil:
		s.text = "No agents running yet."
		return nil
	case instance.DevServer == nil:
		s.text = "No dev server configured.\n\nPress 's' to configure and start a dev server."
		return nil
	case instance.DevServer.Config().DevCommand == "":
		s.text = "No dev server configured.\n\nPress 's' to configure and start a dev server."
		return nil
	}

	server := instance.DevServer

	switch server.Status() {
	case session.DevServerStopped:
		statusText := "Stopped"
		if server.CrashCount() > 0 {
			statusText = fmt.Sprintf("Crashed (%d)", server.CrashCount())
		}
		s.text = lipgloss.JoinVertical(
			lipgloss.Left,
			"Status: "+statusText,
			"",
			"Command: "+server.Config().DevCommand,
			"",
			"Press 's' to start the dev server",
		)
	case session.DevServerBuilding:
		s.text = lipgloss.JoinVertical(
			lipgloss.Left,
			"Status: Building...",
			"",
			"Running: "+server.Config().BuildCommand,
		)
	case session.DevServerStarting:
		s.text = lipgloss.JoinVertical(
			lipgloss.Left,
			"Status: Starting...",
			"",
			"Running: "+server.Config().DevCommand,
		)
	case session.DevServerRunning:
		server.UpdateOutput()
		output := server.Output()
		if output == "" {
			s.text = "Waiting for output..."
		} else {
			s.text = output
		}
	case session.DevServerCrashed:
		output := server.Output()
		if output != "" {
			s.text = lipgloss.JoinVertical(
				lipgloss.Left,
				"Status: Crashed",
				"",
				output,
				"",
				fmt.Sprintf("Press 's' to restart the dev server (crash count: %d)", server.CrashCount()),
			)
		} else {
			s.text = lipgloss.JoinVertical(
				lipgloss.Left,
				"Status: Crashed",
				"",
				"Error: Dev server process has stopped unexpectedly.",
				"",
				fmt.Sprintf("Crash count: %d", server.CrashCount()),
				"",
				"Press 's' to restart the dev server",
			)
		}
	}

	// Update viewport and auto-scroll (only when not in scroll mode)
	if s.viewport.Width > 0 && s.viewport.Height > 0 && !s.isScrolling {
		wasAtBottom := s.viewport.AtBottom()

		s.viewport.SetContent(s.text)

		if wasAtBottom {
			s.viewport.GotoBottom()
			s.userScrolled = false
		}
	}

	return nil
}

func (s *ServerPane) String() string {
	if s.width == 0 || s.height == 0 {
		return strings.Repeat("\n", s.height)
	}

	if s.isScrolling {
		return s.viewport.View()
	}

	wasAtBottom := s.viewport.AtBottom()
	s.viewport.SetContent(s.text)

	if wasAtBottom {
		s.viewport.GotoBottom()
	}

	return s.viewport.View()
}

func (s *ServerPane) ScrollUp() {
	if !s.isScrolling {
		// Entering scroll mode
		s.isScrolling = true
		s.userScrolled = true

		// Add footer for scroll mode
		footer := lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
			Render("ESC to exit scroll mode | ↑↓ to scroll")

		s.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, s.text, footer))
	} else {
		s.userScrolled = true
	}

	s.viewport.LineUp(1)
}

func (s *ServerPane) ScrollDown() {
	if !s.isScrolling {
		// Entering scroll mode
		s.isScrolling = true
		s.userScrolled = true

		footer := lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
			Render("ESC to exit scroll mode | ↑↓ to scroll")

		s.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, s.text, footer))
	} else {
		s.userScrolled = true
	}

	s.viewport.LineDown(1)
}

func (s *ServerPane) ResetToNormalMode() {
	if s.isScrolling {
		s.isScrolling = false
		s.userScrolled = false

		// Remove footer, restore normal content
		s.viewport.SetContent(s.text)

		// Jump to bottom (latest output)
		s.viewport.GotoBottom()
	}
}

func (s *ServerPane) IsScrolling() bool {
	return s.isScrolling
}
