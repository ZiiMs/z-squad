package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool) error {
	home := newHome(ctx, program, autoYes)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Cleanup function to stop all dev servers
	cleanup := func() {
		log.InfoLog.Printf("Cleaning up dev servers on shutdown...")
		for _, instance := range home.list.GetInstances() {
			if instance.DevServer != nil && instance.DevServer.Status() == session.DevServerRunning {
				if err := instance.DevServer.Stop(); err != nil {
					log.ErrorLog.Printf("failed to stop dev server for %s: %v", instance.Title, err)
				}
			}
		}
		// Save instances state
		if err := home.storage.SaveInstances(home.list.GetInstances()); err != nil {
			log.ErrorLog.Printf("failed to save instances on shutdown: %v", err)
		}
	}

	p := tea.NewProgram(
		home,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)

	// Handle signals in a goroutine
	go func() {
		sig := <-sigChan
		log.InfoLog.Printf("Received signal %s, initiating graceful shutdown...", sig)
		cleanup()
		p.Quit()
	}()

	_, err := p.Run()

	// Stop signal handling
	signal.Stop(sigChan)

	// Run cleanup on normal exit too (in case handleQuit wasn't called)
	cleanup()

	return err
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateDevServerConfig is when user is configuring dev server settings.
	stateDevServerConfig
)

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
}

func newHome(ctx context.Context, program string, autoYes bool) *home {
	currentDir, err := filepath.Abs(".")
	if err != nil {
		fmt.Printf("Failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	if err := config.MigrateLegacyState(); err != nil {
		log.ErrorLog.Printf("failed to migrate legacy state: %v", err)
	}

	appConfig := config.LoadConfig()

	appState := config.LoadStateForRepo(currentDir)

	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	h := &home{
		ctx:          ctx,
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane()),
		errBox:       ui.NewErrBox(),
		storage:      storage,
		appConfig:    appConfig,
		program:      program,
		autoYes:      autoYes,
		state:        stateDefault,
		appState:     appState,
	}
	h.list = ui.NewList(&h.spinner, autoYes)

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Add loaded instances to the list
	for _, instance := range instances {
		// Call the finalizer immediately.
		h.list.AddInstance(instance)()
		if autoYes {
			instance.AutoYes = true
		}
	}

	return h
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	// List takes 30% of width, preview takes 70%
	listWidth := int(float32(msg.Width) * 0.3)
	tabsWidth := msg.Width - listWidth

	// Menu takes 10% of height, list and window take 90%
	contentHeight := int(float32(msg.Height) * 0.9)
	menuHeight := msg.Height - contentHeight - 1     // minus 1 for error box
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1) // error box takes 1 row

	m.tabbedWindow.SetSize(tabsWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(int(float32(msg.Width)*0.6), int(float32(msg.Height)*0.4))
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(msg.Width, menuHeight)
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd,
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case previewTickMsg:
		cmd := m.instanceChanged()
		return m, tea.Batch(
			cmd,
			func() tea.Msg {
				time.Sleep(100 * time.Millisecond)
				return previewTickMsg{}
			},
		)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case tickUpdateMetadataMessage:
		for _, instance := range m.list.GetInstances() {
			if !instance.Started() || instance.Paused() {
				continue
			}
			updated, prompt := instance.HasUpdated()
			if updated {
				instance.SetStatus(session.Running)
			} else {
				if prompt {
					instance.TapEnter()
				} else {
					instance.SetStatus(session.Ready)
				}
			}
			if err := instance.UpdateDiffStats(); err != nil {
				log.WarningLog.Printf("could not update diff stats: %v", err)
			}
			// Check dev server health
			if instance.DevServer != nil {
				instance.DevServer.CheckHealth()
			}
		}
		return m, tickUpdateMetadataCmd
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.Status == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.tabbedWindow.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.tabbedWindow.ScrollDown()
				}
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	// Stop all running dev servers before quitting
	for _, instance := range m.list.GetInstances() {
		if instance.DevServer != nil && instance.DevServer.Status() == session.DevServerRunning {
			if err := instance.DevServer.Stop(); err != nil {
				log.ErrorLog.Printf("failed to stop dev server for %s: %v", instance.Title, err)
			}
		}
	}

	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateDevServerConfig {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter on stateNew, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNew {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	if m.state == stateNew {
		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Type {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}

			if err := instance.Start(true); err != nil {
				m.list.Kill()
				m.state = stateDefault
				return m, m.handleError(err)
			}
			// Save after adding new instance
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			// Instance added successfully, call the finalizer.
			m.newInstanceFinalizer()
			if m.autoYes {
				instance.AutoYes = true
			}

			m.newInstanceFinalizer()
			m.state = stateDefault
			if m.promptAfterName {
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				// Initialize the text input overlay
				m.textInputOverlay = overlay.NewTextInputOverlay("Enter prompt", "")
				m.promptAfterName = false
			} else {
				m.menu.SetState(ui.StateDefault)
				m.showHelpScreen(helpStart(instance), nil)
			}

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyEsc:
			m.list.Kill()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Use the new TextInputOverlay component to handle all key events
		shouldClose := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			// TODO: this should never happen since we set the instance in the previous state.
			if selected == nil {
				return m, nil
			}
			if m.textInputOverlay.IsSubmitted() {
				if err := selected.SendPrompt(m.textInputOverlay.GetValue()); err != nil {
					// TODO: we probably end up in a bad state here.
					return m, m.handleError(err)
				}
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					m.showHelpScreen(helpStart(selected), nil)
					return nil
				},
			)
		}
		return m, nil
	} else if m.state == stateDevServerConfig {
		// Dev server configuration overlay - callback handles all state transitions
		if m.textInputOverlay == nil {
			m.state = stateDefault
			return m, nil
		}
		m.textInputOverlay.HandleKeyPress(msg)
		return m, nil
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			return m, nil
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
		// If in preview tab and in scroll mode, exit scroll mode
		if !m.tabbedWindow.IsInDiffTab() && !m.tabbedWindow.IsInServerTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in server tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInServerTab() && m.tabbedWindow.IsServerInScrollMode() {
			m.tabbedWindow.ResetServerToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyPrompt:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, nil
	case keys.KeyNew:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)

		return m, nil
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.tabbedWindow.Toggle()
		m.menu.SetInDiffTab(m.tabbedWindow.IsInDiffTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}

		// Create the kill action as a tea.Cmd
		killAction := func() tea.Msg {
			// Get worktree and check if branch is checked out
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}

			checkedOut, err := worktree.IsBranchCheckedOut()
			if err != nil {
				return err
			}

			if checkedOut {
				return fmt.Errorf("instance %s is currently checked out", selected.Title)
			}

			// Delete from storage first
			if err := m.storage.DeleteInstance(selected.Title); err != nil {
				return err
			}

			// Then kill the instance
			m.list.Kill()
			return instanceChangedMsg{}
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
		return m, m.confirmAction(message, killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}

		// Show help screen before pausing
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			if err := selected.Pause(); err != nil {
				m.handleError(err)
			}
			m.instanceChanged()
		})
		return m, nil
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.WindowSize()
	case keys.KeyDevServerStart:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}
		return m, m.handleDevServerStart(selected)
	case keys.KeyDevServerStop:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}
		return m, m.handleDevServerStop(selected)
	case keys.KeyDevServerEdit:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return m, nil
		}
		return m, m.handleDevServerEdit(selected)
	case keys.KeyEnter:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() {
			return m, nil
		}

		// Check which tab is active to determine what to attach to
		if m.tabbedWindow.IsInServerTab() {
			// Server tab - attach to dev server session
			return m, m.handleDevServerAttach(selected)
		} else {
			// Preview/Diff tab - attach to instance session (existing behavior)
			if !selected.TmuxAlive() {
				return m, nil
			}
			m.showHelpScreen(helpTypeInstanceAttach{}, func() {
				ch, err := m.list.Attach()
				if err != nil {
					m.handleError(err)
					return
				}
				<-ch
				m.state = stateDefault
			})
			return m, nil
		}
	default:
		return m, nil
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}

	// Update server pane if dev server is running
	if selected != nil && selected.DevServer != nil {
		if err := m.tabbedWindow.UpdateServer(selected); err != nil {
			return m.handleError(err)
		}
	}

	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type tickUpdateMetadataMessage struct{}

type instanceChangedMsg struct{}

// tickUpdateMetadataCmd is the callback to update the metadata of the instances every 500ms. Note that we iterate
// overall the instances and capture their output. It's a pretty expensive operation. Let's do it 2x a second only.
var tickUpdateMetadataCmd = func() tea.Msg {
	time.Sleep(500 * time.Millisecond)
	return tickUpdateMetadataMessage{}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Execute the action if it exists
		if action != nil {
			_ = action()
		}
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
	}

	return nil
}

func (m *home) handleDevServerStart(instance *session.Instance) tea.Cmd {
	// GUARD: Check if server is already active
	if instance.DevServer != nil {
		status := instance.DevServer.Status()
		if status == session.DevServerRunning ||
			status == session.DevServerStarting ||
			status == session.DevServerBuilding {
			log.InfoLog.Printf("Dev server already active (status=%v), ignoring start request", status)
			return nil
		}
	}

	worktreePath := ""
	repoPath := ""

	// Try to get worktree path from gitWorktree
	worktree, err := instance.GetGitWorktree()
	if err == nil && worktree != nil {
		worktreePath = worktree.GetWorktreePath()
		repoPath = worktree.GetRepoPath()
	}

	// Fallback to instance path
	if worktreePath == "" {
		worktreePath = instance.Path
	}
	if repoPath == "" {
		// Try to get repo path from worktree path
		repoPath = worktreePath
	}

	log.InfoLog.Printf("handleDevServerStart: worktreePath=%s, repoPath=%s", worktreePath, repoPath)

	if instance.DevServer == nil {
		// Load settings from main repo (project-wide settings)
		settings, err := config.LoadDevServerSettings(repoPath)
		if err != nil {
			return m.handleError(err)
		}

		if settings == nil || settings.DevCommand == "" {
			return m.showDevServerConfigOverlay(instance, repoPath)
		}

		instance.DevServer = session.NewDevServer(
			session.DevServerConfig{
				BuildCommand: settings.BuildCommand,
				DevCommand:   settings.DevCommand,
				Env:          settings.Env,
			},
			worktreePath,
			instance.Title,
		)
	}

	if err := instance.DevServer.Start(); err != nil {
		return m.handleError(err)
	}

	return m.instanceChanged()
}

func (m *home) handleDevServerStop(instance *session.Instance) tea.Cmd {
	if instance.DevServer == nil {
		return nil
	}

	if err := instance.DevServer.Stop(); err != nil {
		return m.handleError(err)
	}

	return m.instanceChanged()
}

func (m *home) handleDevServerAttach(instance *session.Instance) tea.Cmd {
	// Check if dev server exists and is running
	if instance.DevServer == nil {
		return m.handleError(fmt.Errorf("no dev server configured"))
	}

	status := instance.DevServer.Status()
	if status != session.DevServerRunning {
		return m.handleError(fmt.Errorf("dev server is not running (status: %v)", status))
	}

	// Check if dev server session exists
	if !instance.DevServer.SessionExists() {
		return m.handleError(fmt.Errorf("dev server session does not exist"))
	}

	// Get the dev server tmux session
	devServerSession := instance.DevServer.GetDevServerSession()
	if devServerSession == nil {
		return m.handleError(fmt.Errorf("dev server session is nil"))
	}

	// Show help screen before attaching
	m.showHelpScreen(helpTypeServerAttach{}, func() {
		ch, err := devServerSession.Attach()
		if err != nil {
			m.handleError(err)
			return
		}
		<-ch
		m.state = stateDefault
	})

	return nil
}

func (m *home) handleDevServerEdit(instance *session.Instance) tea.Cmd {
	worktreePath := ""
	repoPath := ""

	// Try to get worktree path from gitWorktree
	worktree, err := instance.GetGitWorktree()
	if err == nil && worktree != nil {
		worktreePath = worktree.GetWorktreePath()
		repoPath = worktree.GetRepoPath()
	}

	// Fallback to instance path
	if worktreePath == "" {
		worktreePath = instance.Path
	}
	if repoPath == "" {
		repoPath = instance.Path
	}

	// Stop the dev server if running
	if instance.DevServer != nil && instance.DevServer.IsRunning() {
		instance.DevServer.Stop()
	}

	// Load existing settings (or defaults)
	settings, _ := config.LoadDevServerSettings(repoPath)
	if settings == nil {
		settings = &config.DevServerSettings{
			BuildCommand: "",
			DevCommand:   "",
			Env:          make(map[string]string),
		}
	}

	m.state = stateDevServerConfig
	m.textInputOverlay = overlay.NewTextInputOverlay("Build command (empty to skip):", settings.BuildCommand)
	m.textInputOverlay.SetOnSubmit(func() {
		buildCmd := m.textInputOverlay.GetValue()

		m.textInputOverlay = overlay.NewTextInputOverlay("Dev server command:", settings.DevCommand)
		m.textInputOverlay.SetOnSubmit(func() {
			devCmd := m.textInputOverlay.GetValue()

			newSettings := &config.DevServerSettings{
				BuildCommand: buildCmd,
				DevCommand:   devCmd,
				Env:          make(map[string]string),
			}

			// Save settings to main repo (project-wide)
			if err := config.SaveDevServerSettings(newSettings, repoPath); err != nil {
				m.handleError(err)
				return
			}

			instance.DevServer = session.NewDevServer(
				session.DevServerConfig{
					BuildCommand: buildCmd,
					DevCommand:   devCmd,
					Env:          newSettings.Env,
				},
				worktreePath,
				instance.Title,
			)

			m.state = stateDefault
			m.textInputOverlay = nil
			m.instanceChanged()
		})
	})

	return nil
}

type devServerConfigState int

const (
	devServerConfigBuild devServerConfigState = iota
	devServerConfigDev
	devServerConfigDone
)

type devServerConfigOverlay struct {
	state       devServerConfigState
	buildCmd    string
	devCmd      string
	instance    *session.Instance
	repoPath    string
	worktree    string
	textOverlay *overlay.TextInputOverlay
}

func (m *home) showDevServerConfigOverlay(instance *session.Instance, repoPath string) tea.Cmd {
	worktreePath := ""
	worktree, err := instance.GetGitWorktree()
	if err == nil && worktree != nil {
		worktreePath = worktree.GetWorktreePath()
	}
	if worktreePath == "" {
		worktreePath = instance.Path
	}

	m.state = stateDevServerConfig
	m.textInputOverlay = overlay.NewTextInputOverlay("Build command (empty to skip):", "")
	m.textInputOverlay.SetOnSubmit(func() {
		buildCmd := m.textInputOverlay.GetValue()

		m.textInputOverlay = overlay.NewTextInputOverlay("Dev server command:", "")
		m.textInputOverlay.SetOnSubmit(func() {
			devCmd := m.textInputOverlay.GetValue()

			settings := &config.DevServerSettings{
				BuildCommand: buildCmd,
				DevCommand:   devCmd,
				Env:          make(map[string]string),
			}

			// Save settings to main repo (project-wide)
			if err := config.SaveDevServerSettings(settings, repoPath); err != nil {
				m.handleError(err)
				return
			}

			instance.DevServer = session.NewDevServer(
				session.DevServerConfig{
					BuildCommand: buildCmd,
					DevCommand:   devCmd,
					Env:          settings.Env,
				},
				worktreePath,
				instance.Title,
			)

			m.state = stateDefault
			m.textInputOverlay = nil
			m.handleDevServerStart(instance)
		})
	})

	return nil
}

func (m *home) View() string {
	listWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.list.String())
	previewWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.tabbedWindow.String())
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, listWithPadding, previewWithPadding)

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)

	if m.state == statePrompt || m.state == stateDevServerConfig {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true)
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true)
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true)
	}

	return mainView
}
