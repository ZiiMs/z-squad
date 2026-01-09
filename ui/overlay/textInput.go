package overlay

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TextInputOverlay represents a text input overlay with state management.
type TextInputOverlay struct {
	textinput     textinput.Model
	Title         string
	Submitted     bool
	Canceled      bool
	OnSubmit      func()
	width, height int
}

// NewTextInputOverlay creates a new text input overlay with the given title and initial value.
func NewTextInputOverlay(title string, initialValue string) *TextInputOverlay {
	ti := textinput.New()
	ti.SetValue(initialValue)
	ti.Focus()
	ti.CharLimit = 0
	ti.Prompt = ""
	ti.CursorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("62"))

	return &TextInputOverlay{
		textinput: ti,
		Title:     title,
		Submitted: false,
		Canceled:  false,
	}
}

func (t *TextInputOverlay) SetSize(width, height int) {
	t.textinput.Width = width - 6
	t.width = width
	t.height = height
}

// Init initializes the text input overlay model
func (t *TextInputOverlay) Init() tea.Cmd {
	return nil
}

// View renders the model's view
func (t *TextInputOverlay) View() string {
	return t.Render()
}

// HandleKeyPress processes a key press and updates the state accordingly.
// Returns true if the overlay should be closed.
func (t *TextInputOverlay) HandleKeyPress(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		t.Canceled = true
		return true
	case tea.KeyEnter:
		t.Submitted = true
		if t.OnSubmit != nil {
			t.OnSubmit()
		}
		return true
	default:
		t.textinput, _ = t.textinput.Update(msg)
		return false
	}
}

// GetValue returns the current value of the text input.
func (t *TextInputOverlay) GetValue() string {
	return t.textinput.Value()
}

// IsSubmitted returns whether the form was submitted.
func (t *TextInputOverlay) IsSubmitted() bool {
	return t.Submitted
}

// IsCanceled returns whether the form was canceled.
func (t *TextInputOverlay) IsCanceled() bool {
	return t.Canceled
}

// SetOnSubmit sets a callback function for form submission.
func (t *TextInputOverlay) SetOnSubmit(onSubmit func()) {
	t.OnSubmit = onSubmit
}

// Render renders the text input overlay.
func (t *TextInputOverlay) Render() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2)

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("62")).
		Bold(true).
		MarginBottom(1)

	content := titleStyle.Render(t.Title) + "\n"
	content += t.textinput.View() + "\n\n"
	content += " Enter to submit • Esc to cancel "

	return style.Render(content)
}
