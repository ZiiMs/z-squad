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
	width       int
	height      int
	text        string
	viewport    viewport.Model
	isScrolling bool
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

	return nil
}

func (s *ServerPane) String() string {
	if s.width == 0 || s.height == 0 {
		return strings.Repeat("\n", s.height)
	}

	if s.isScrolling {
		return s.viewport.View()
	}

	availableHeight := s.height - 1

	lines := strings.Split(s.text, "\n")

	if availableHeight > 0 {
		if len(lines) > availableHeight {
			// Show bottom of content (most recent output) with ellipsis at top
			// Take (availableHeight - 1) lines from the bottom, then prepend ellipsis
			lines = append([]string{"..."}, lines[len(lines)-availableHeight+1:]...)
		} else {
			padding := availableHeight - len(lines)
			lines = append(lines, make([]string, padding)...)
		}
	}

	content := strings.Join(lines, "\n")
	rendered := serverPaneStyle.Width(s.width).Render(content)
	return rendered
}

func (s *ServerPane) ScrollUp() {
	if !s.isScrolling {
		s.isScrolling = true
		content := s.text
		footer := lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
			Render("ESC to exit scroll mode")

		s.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, content, footer))
		s.viewport.GotoBottom()
	} else {
		s.viewport.LineUp(1)
	}
}

func (s *ServerPane) ScrollDown() {
	if !s.isScrolling {
		s.isScrolling = true
		content := s.text
		footer := lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}).
			Render("ESC to exit scroll mode")

		s.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, content, footer))
		s.viewport.GotoBottom()
	} else {
		s.viewport.LineDown(1)
	}
}

func (s *ServerPane) ResetToNormalMode() {
	if s.isScrolling {
		s.isScrolling = false
		s.viewport.SetContent("")
		s.viewport.GotoTop()
	}
}

func (s *ServerPane) IsScrolling() bool {
	return s.isScrolling
}
