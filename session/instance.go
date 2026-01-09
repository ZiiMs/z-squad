package session

import (
	"claude-squad/log"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"path/filepath"

	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
)

type Status int

const (
	// Running is the status when the instance is running and claude is working.
	Running Status = iota
	// Ready is if the claude instance is ready to be interacted with (waiting for user input).
	Ready
	// Loading is if the instance is loading (if we are starting it up or something).
	Loading
	// Paused is if the instance is paused (worktree removed but branch preserved).
	Paused
)

// DevServerStatus represents the current state of a dev server
type DevServerStatus int

const (
	// DevServerStopped is when the dev server is not running
	DevServerStopped DevServerStatus = iota
	// DevServerBuilding is when the build command is running
	DevServerBuilding
	// DevServerStarting is when the dev server is starting up
	DevServerStarting
	// DevServerRunning is when the dev server is running and healthy
	DevServerRunning
	// DevServerCrashed is when the dev server process has crashed
	DevServerCrashed
)

// DevServerConfig holds configuration for a dev server
type DevServerConfig struct {
	BuildCommand string            `json:"build_command"`
	DevCommand   string            `json:"dev_command"`
	Env          map[string]string `json:"env,omitempty"`
}

// DevServer manages the dev server process for an instance
type DevServer struct {
	config     DevServerConfig
	status     DevServerStatus
	session    *tmux.TmuxSession
	crashCount int
	output     []string
	outputMu   sync.RWMutex
	worktree   string
	instance   string
}

// Instance is a running instance of claude code.
type Instance struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// Program is the program to run in the instance.
	Program string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// AutoYes is true if the instance should automatically press enter when prompted.
	AutoYes bool
	// Prompt is the initial prompt to pass to the instance on startup
	Prompt string

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// DevServer holds the dev server (from devserver package)
	DevServer interface {
		Status() DevServerStatus
		Config() DevServerConfig
		CrashCount() int
		Output() string
		IsRunning() bool
		SessionExists() bool
		UpdateOutput()
		Start() error
		Stop() error
		CheckHealth()
	}

	// The below fields are initialized upon calling Start().

	started bool
	// tmuxSession is the tmux session for the instance.
	tmuxSession *tmux.TmuxSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree
}

// ToInstanceData converts an Instance to its serializable form
func (i *Instance) ToInstanceData() InstanceData {
	data := InstanceData{
		Title:     i.Title,
		Path:      i.Path,
		Branch:    i.Branch,
		Status:    i.Status,
		Height:    i.Height,
		Width:     i.Width,
		CreatedAt: i.CreatedAt,
		UpdatedAt: time.Now(),
		Program:   i.Program,
		AutoYes:   i.AutoYes,
	}

	// Only include worktree data if gitWorktree is initialized
	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:      i.gitWorktree.GetRepoPath(),
			WorktreePath:  i.gitWorktree.GetWorktreePath(),
			SessionName:   i.Title,
			BranchName:    i.gitWorktree.GetBranchName(),
			BaseCommitSHA: i.gitWorktree.GetBaseCommitSHA(),
		}
	}

	// Only include diff stats if they exist
	if i.diffStats != nil {
		data.DiffStats = DiffStatsData{
			Added:   i.diffStats.Added,
			Removed: i.diffStats.Removed,
			Content: i.diffStats.Content,
		}
	}

	// Include dev server data if it exists
	if i.DevServer != nil {
		data.DevServer = &DevServerData{
			Config:     i.DevServer.Config(),
			Status:     i.DevServer.Status(),
			CrashCount: i.DevServer.CrashCount(),
		}
	}

	return data
}

// FromInstanceData creates a new Instance from serialized data
func FromInstanceData(data InstanceData) (*Instance, error) {
	instance := &Instance{
		Title:     data.Title,
		Path:      data.Path,
		Branch:    data.Branch,
		Status:    data.Status,
		Height:    data.Height,
		Width:     data.Width,
		CreatedAt: data.CreatedAt,
		UpdatedAt: data.UpdatedAt,
		Program:   data.Program,
		gitWorktree: git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
		),
		diffStats: &git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		},
	}

	// Restore dev server data if it exists
	if data.DevServer != nil {
		instance.DevServer = &DevServer{
			config:     data.DevServer.Config,
			status:     data.DevServer.Status,
			crashCount: data.DevServer.CrashCount,
			output:     make([]string, 0),
			worktree:   instance.gitWorktree.GetWorktreePath(),
			instance:   instance.Title,
		}
	}

	if instance.Paused() {
		instance.started = true
		instance.tmuxSession = tmux.NewTmuxSession(instance.Title, instance.Program)
	} else {
		if err := instance.Start(false); err != nil {
			return nil, err
		}
	}

	return instance, nil
}

// Options for creating a new instance
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// If AutoYes is true, then
	AutoYes bool
}

func NewInstance(opts InstanceOptions) (*Instance, error) {
	t := time.Now()

	// Convert path to absolute
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &Instance{
		Title:     opts.Title,
		Status:    Ready,
		Path:      absPath,
		Program:   opts.Program,
		Height:    0,
		Width:     0,
		CreatedAt: t,
		UpdatedAt: t,
		AutoYes:   false,
	}, nil
}

func (i *Instance) RepoName() (string, error) {
	if !i.started {
		return "", fmt.Errorf("cannot get repo name for instance that has not been started")
	}
	return i.gitWorktree.GetRepoName(), nil
}

func (i *Instance) SetStatus(status Status) {
	i.Status = status
}

// firstTimeSetup is true if this is a new instance. Otherwise, it's one loaded from storage.
func (i *Instance) Start(firstTimeSetup bool) error {
	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	var tmuxSession *tmux.TmuxSession
	if i.tmuxSession != nil {
		// Use existing tmux session (useful for testing)
		tmuxSession = i.tmuxSession
	} else {
		// Create new tmux session
		tmuxSession = tmux.NewTmuxSession(i.Title, i.Program)
	}
	i.tmuxSession = tmuxSession

	if firstTimeSetup {
		gitWorktree, branchName, err := git.NewGitWorktree(i.Path, i.Title)
		if err != nil {
			return fmt.Errorf("failed to create git worktree: %w", err)
		}
		i.gitWorktree = gitWorktree
		i.Branch = branchName
	}

	// Setup error handler to cleanup resources on any error
	var setupErr error
	defer func() {
		if setupErr != nil {
			if cleanupErr := i.Kill(); cleanupErr != nil {
				setupErr = fmt.Errorf("%v (cleanup error: %v)", setupErr, cleanupErr)
			}
		} else {
			i.started = true
		}
	}()

	if !firstTimeSetup {
		// Reuse existing session
		if err := tmuxSession.Restore(); err != nil {
			setupErr = fmt.Errorf("failed to restore existing session: %w", err)
			return setupErr
		}
	} else {
		// Setup git worktree first
		if err := i.gitWorktree.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Create new session
		if err := i.tmuxSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	}

	i.SetStatus(Running)

	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() error {
	if !i.started {
		// If instance was never started, just return success
		return nil
	}

	var errs []error

	// Stop dev server first if it's running
	if i.DevServer != nil {
		if err := i.DevServer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop dev server: %w", err))
		}
	}

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if i.tmuxSession != nil {
		if err := i.tmuxSession.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
	}

	// Then clean up git worktree
	if i.gitWorktree != nil {
		if err := i.gitWorktree.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error
func (i *Instance) combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple cleanup errors occurred:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", errMsg)
}

func (i *Instance) Preview() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.tmuxSession.CapturePaneContent()
}

func (i *Instance) HasUpdated() (updated bool, hasPrompt bool) {
	if !i.started {
		return false, false
	}
	return i.tmuxSession.HasUpdated()
}

// TapEnter sends an enter key press to the tmux session if AutoYes is enabled.
func (i *Instance) TapEnter() {
	if !i.started || !i.AutoYes {
		return
	}
	if err := i.tmuxSession.TapEnter(); err != nil {
		log.ErrorLog.Printf("error tapping enter: %v", err)
	}
}

func (i *Instance) Attach() (chan struct{}, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot attach instance that has not been started")
	}
	return i.tmuxSession.Attach()
}

func (i *Instance) SetPreviewSize(width, height int) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.tmuxSession.SetDetachedSize(width, height)
}

// GetGitWorktree returns the git worktree for the instance
func (i *Instance) GetGitWorktree() (*git.GitWorktree, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot get git worktree for instance that has not been started")
	}
	return i.gitWorktree, nil
}

func (i *Instance) Started() bool {
	return i.started
}

// SetTitle sets the title of the instance. Returns an error if the instance has started.
// We cant change the title once it's been used for a tmux session etc.
func (i *Instance) SetTitle(title string) error {
	if i.started {
		return fmt.Errorf("cannot change title of a started instance")
	}
	i.Title = title
	return nil
}

func (i *Instance) Paused() bool {
	return i.Status == Paused
}

// TmuxAlive returns true if the tmux session is alive. This is a sanity check before attaching.
func (i *Instance) TmuxAlive() bool {
	return i.tmuxSession.DoesSessionExist()
}

// Pause stops the tmux session and removes the worktree, preserving the branch
func (i *Instance) Pause() error {
	if !i.started {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	if i.Status == Paused {
		return fmt.Errorf("instance is already paused")
	}

	var errs []error

	// Check if there are any changes to commit
	if dirty, err := i.gitWorktree.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
		log.ErrorLog.Print(err)
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := i.gitWorktree.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			log.ErrorLog.Print(err)
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Detach from tmux session instead of closing to preserve session output
	if err := i.tmuxSession.DetachSafely(); err != nil {
		errs = append(errs, fmt.Errorf("failed to detach tmux session: %w", err))
		log.ErrorLog.Print(err)
		// Continue with pause process even if detach fails
	}

	// Note: We intentionally do NOT remove the worktree here.
	// Keeping the worktree directory preserves the tmux session's working directory,
	// allowing opencode (and other AI agents) to maintain their conversation context.
	// The worktree will only be cleaned up when the instance is killed.
	// Users can cd into the worktree directory (path copied to clipboard) to run dev servers or test changes.

	if err := i.combineErrors(errs); err != nil {
		log.ErrorLog.Print(err)
		return err
	}

	i.SetStatus(Paused)
	_ = clipboard.WriteAll(i.gitWorktree.GetWorktreePath())
	return nil
}

// Resume recreates the worktree and restarts the tmux session
func (i *Instance) Resume() error {
	if !i.started {
		return fmt.Errorf("cannot resume instance that has not been started")
	}
	if i.Status != Paused {
		return fmt.Errorf("can only resume paused instances")
	}

	// Check if branch is checked out
	if checked, err := i.gitWorktree.IsBranchCheckedOut(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to check if branch is checked out: %w", err)
	} else if checked {
		return fmt.Errorf("cannot resume: branch is checked out, please switch to a different branch")
	}

	// Check if worktree directory exists (it should exist after pause since we don't remove it)
	if _, err := os.Stat(i.gitWorktree.GetWorktreePath()); os.IsNotExist(err) {
		// Worktree was removed externally, need to recreate it
		if err := i.gitWorktree.Setup(); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to setup git worktree: %w", err)
		}
	} else if err != nil {
		// Error checking if worktree exists
		return fmt.Errorf("failed to check if worktree exists: %w", err)
	}
	// Note: If worktree exists, we don't call Setup() to preserve tmux session's working directory

	// Check if tmux session still exists from pause, otherwise create new one
	if i.tmuxSession.DoesSessionExist() {
		// Session exists, just restore PTY connection to it
		if err := i.tmuxSession.Restore(); err != nil {
			log.ErrorLog.Print(err)
			// If restore fails, fall back to creating new session
			if err := i.tmuxSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
				log.ErrorLog.Print(err)
				// Cleanup git worktree if tmux session creation fails
				if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
					err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
					log.ErrorLog.Print(err)
				}
				return fmt.Errorf("failed to start new session: %w", err)
			}
		}
	} else {
		// Create new tmux session
		if err := i.tmuxSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
			log.ErrorLog.Print(err)
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
				log.ErrorLog.Print(err)
			}
			return fmt.Errorf("failed to start new session: %w", err)
		}
	}

	i.SetStatus(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	if !i.started {
		i.diffStats = nil
		return nil
	}

	if i.Status == Paused {
		// Keep the previous diff stats if the instance is paused
		return nil
	}

	stats := i.gitWorktree.Diff()
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			// Worktree is not fully set up yet, not an error
			i.diffStats = nil
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}

	i.diffStats = stats
	return nil
}

// GetDiffStats returns the current git diff statistics
func (i *Instance) GetDiffStats() *git.DiffStats {
	return i.diffStats
}

// SendPrompt sends a prompt to the tmux session
func (i *Instance) SendPrompt(prompt string) error {
	if !i.started {
		return fmt.Errorf("instance not started")
	}
	if i.tmuxSession == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	if err := i.tmuxSession.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to tmux session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := i.tmuxSession.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}

// PreviewFullHistory captures the entire tmux pane output including full scrollback history
func (i *Instance) PreviewFullHistory() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.tmuxSession.CapturePaneContentWithOptions("-", "-")
}

// SetTmuxSession sets the tmux session for testing purposes
func (i *Instance) SetTmuxSession(session *tmux.TmuxSession) {
	i.tmuxSession = session
}

// SendKeys sends keys to the tmux session
func (i *Instance) SendKeys(keys string) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.tmuxSession.SendKeys(keys)
}

// NewDevServer creates a new DevServer with the given configuration
func NewDevServer(config DevServerConfig, worktree string, instance string) *DevServer {
	return &DevServer{
		config:   config,
		status:   DevServerStopped,
		output:   make([]string, 0),
		worktree: worktree,
		instance: instance,
	}
}

// SetDevServerSession sets the tmux session for the dev server
func (d *DevServer) SetDevServerSession(session *tmux.TmuxSession) {
	d.session = session
}

// GetDevServerSession returns the dev server tmux session
func (d *DevServer) GetDevServerSession() *tmux.TmuxSession {
	return d.session
}

// GetOutput returns the current dev server output
func (d *DevServer) GetOutput() string {
	d.outputMu.RLock()
	defer d.outputMu.RUnlock()
	return strings.Join(d.output, "\n")
}

// appendOutput adds a line to the output buffer (max 100 lines)
func (d *DevServer) appendOutput(line string) {
	d.outputMu.Lock()
	defer d.outputMu.Unlock()
	d.output = append(d.output, line)
	if len(d.output) > 100 {
		d.output = d.output[len(d.output)-100:]
	}
}

// UpdateOutputFromSession captures output from the tmux session
func (d *DevServer) UpdateOutputFromSession() error {
	if d.session == nil {
		return nil
	}
	content, err := d.session.CapturePaneContent()
	if err != nil {
		return err
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			d.appendOutput(line)
		}
	}
	return nil
}

// IsRunning returns true if the dev server is currently running
func (d *DevServer) IsRunning() bool {
	return d.Status() == DevServerRunning || d.Status() == DevServerStarting
}

// SessionExists returns true if the tmux session exists and is responsive
func (d *DevServer) SessionExists() bool {
	if d.session == nil {
		log.InfoLog.Printf("DevServer.SessionExists: session is nil")
		return false
	}
	doesExist := d.session.DoesSessionExist()
	log.InfoLog.Printf("DevServer.SessionExists: tmux DoesSessionExist = %v", doesExist)
	if !doesExist {
		return false
	}
	// Try to capture content to verify the session is responsive
	content, err := d.session.CapturePaneContent()
	if err != nil {
		log.InfoLog.Printf("DevServer.SessionExists: CapturePaneContent error = %v", err)
		return false
	}
	log.InfoLog.Printf("DevServer.SessionExists: captured %d bytes", len(content))
	// If we can capture content, session is alive
	return content != ""
}

// CheckHealth checks if the dev server is still running
func (d *DevServer) CheckHealth() {
	if d.status != DevServerRunning {
		return
	}

	// Update output first to capture any final messages
	d.UpdateOutputFromSession()

	if d.session == nil {
		log.InfoLog.Printf("Dev server session is nil, marking as crashed")
		d.crashCount++
		d.status = DevServerCrashed
		d.appendOutput(fmt.Sprintf("[%s] Dev server crashed! Session was nil.", time.Now().Format("15:04:05")))
		return
	}

	sessionExists := d.session.DoesSessionExist()
	log.InfoLog.Printf("Dev server session exists check: %v", sessionExists)

	if !sessionExists {
		log.InfoLog.Printf("Dev server session doesn't exist, marking as crashed")
		d.crashCount++
		d.status = DevServerCrashed
		output := d.Output()
		if output != "" {
			lastLines := strings.Split(output, "\n")
			numLines := len(lastLines)
			start := 0
			if numLines > 20 {
				start = numLines - 20
			}
			d.appendOutput(fmt.Sprintf("[%s] Dev server crashed! Last output:", time.Now().Format("15:04:05")))
			for i := start; i < numLines; i++ {
				d.appendOutput("  " + lastLines[i])
			}
		} else {
			d.appendOutput(fmt.Sprintf("[%s] Dev server crashed! No output available.", time.Now().Format("15:04:05")))
		}
		d.appendOutput(fmt.Sprintf("Crash count: %d", d.crashCount))
		if d.crashCount >= 3 {
			d.appendOutput("Multiple crashes detected. Check your dev server configuration.")
		}
	}
}

// HasUpdated checks if the dev server output has changed
func (d *DevServer) HasUpdated() bool {
	if d.session == nil {
		return false
	}
	prevLen := len(d.output)
	d.UpdateOutputFromSession()
	return len(d.output) > prevLen
}

// Status returns the current dev server status
func (d *DevServer) Status() DevServerStatus {
	return d.status
}

// SetStatus sets the dev server status
func (d *DevServer) SetStatus(status DevServerStatus) {
	d.status = status
}

// Config returns the dev server configuration
func (d *DevServer) Config() DevServerConfig {
	return d.config
}

// CrashCount returns the number of times the dev server has crashed
func (d *DevServer) CrashCount() int {
	return d.crashCount
}

// IncrementCrashCount increments the crash count
func (d *DevServer) IncrementCrashCount() {
	d.crashCount++
}

// UpdateOutput updates the output from the tmux session
func (d *DevServer) UpdateOutput() {
	d.UpdateOutputFromSession()
}

// Output returns the current dev server output
func (d *DevServer) Output() string {
	d.outputMu.RLock()
	defer d.outputMu.RUnlock()
	return strings.Join(d.output, "\n")
}

// Start starts the dev server
func (d *DevServer) Start() error {
	log.InfoLog.Printf("DevServer.Start: called")

	if d.IsRunning() {
		log.InfoLog.Printf("DevServer.Start: already running")
		return fmt.Errorf("dev server is already running")
	}

	if d.config.DevCommand == "" {
		return fmt.Errorf("dev command not configured")
	}

	d.status = DevServerBuilding
	log.InfoLog.Printf("DevServer.Start: status = Building")

	if d.config.BuildCommand != "" {
		log.InfoLog.Printf("DevServer.Start: running build command: %s", d.config.BuildCommand)
		if err := d.runBuild(); err != nil {
			log.ErrorLog.Printf("DevServer.Start: build failed: %v", err)
			d.status = DevServerStopped
			return fmt.Errorf("build failed: %w", err)
		}
		log.InfoLog.Printf("DevServer.Start: build completed")
	}

	d.status = DevServerStarting
	log.InfoLog.Printf("DevServer.Start: status = Starting")

	if err := d.startDevServer(); err != nil {
		log.ErrorLog.Printf("DevServer.Start: startDevServer failed: %v", err)
		d.status = DevServerStopped
		return fmt.Errorf("failed to start dev server: %w", err)
	}

	d.status = DevServerRunning
	log.InfoLog.Printf("DevServer.Start: status = Running, dev server started successfully")

	return nil
}

// Stop stops the dev server
func (d *DevServer) Stop() error {
	if d.session == nil {
		d.status = DevServerStopped
		return nil
	}

	d.session.SendKeys("\x03")
	time.Sleep(2 * time.Second)

	if d.session.DoesSessionExist() {
		d.session.Close()
	}

	d.session = nil
	d.status = DevServerStopped
	return nil
}

// runBuild runs the build command
func (d *DevServer) runBuild() error {
	cmd := exec.Command("sh", "-c", d.config.BuildCommand)
	output, err := cmd.Output()
	if err != nil {
		d.appendOutput(string(output))
		return fmt.Errorf("build command failed: %w", err)
	}
	d.appendOutput(string(output))
	return nil
}

// startDevServer starts the dev server in a tmux session
func (d *DevServer) startDevServer() error {
	log.InfoLog.Printf("startDevServer: d.worktree = '%s'", d.worktree)
	log.InfoLog.Printf("startDevServer: d.instance = '%s'", d.instance)
	log.InfoLog.Printf("startDevServer: d.config.DevCommand = '%s'", d.config.DevCommand)

	sessionName := devServerSessionName(d.instance)
	fullSessionName := fmt.Sprintf("%s%s", tmux.TmuxPrefix, sessionName)

	log.InfoLog.Printf("Starting dev server session: %s (sessionName=%s)", fullSessionName, sessionName)
	log.InfoLog.Printf("Dev command: %s", d.config.DevCommand)
	log.InfoLog.Printf("Worktree: %s", d.worktree)

	if exec.Command("tmux", "has-session", "-t", fullSessionName).Run() == nil {
		log.InfoLog.Printf("Killing existing session: %s", fullSessionName)
		exec.Command("tmux", "kill-session", "-t", fullSessionName).Run()
	}

	// Build the dev command with optional environment variables
	var devCmd string
	if len(d.config.Env) > 0 {
		envParts := make([]string, 0, len(d.config.Env))
		for k, v := range d.config.Env {
			envParts = append(envParts, fmt.Sprintf("%s=%s", k, v))
		}
		envPrefix := strings.Join(envParts, " ")
		devCmd = fmt.Sprintf("%s %s", envPrefix, d.config.DevCommand)
	} else {
		devCmd = d.config.DevCommand
	}

	log.InfoLog.Printf("Full command: %s (in dir: %s)", devCmd, d.worktree)

	// Use -c to set working directory instead of cd && pattern
	tmuxCmd := exec.Command("tmux", "new-session", "-d", "-s", fullSessionName, "-c", d.worktree, "-x", "200", "-y", "50", "sh", "-c", devCmd)

	ptmx, err := tmux.MakePtyFactory().Start(tmuxCmd)
	if err != nil {
		log.ErrorLog.Printf("Failed to start tmux session: %v", err)
		return fmt.Errorf("failed to start tmux session: %w", err)
	}

	// Create TmuxSession object first so we can use DoesSessionExist
	d.session = tmux.NewTmuxSession(fullSessionName, d.config.DevCommand)

	// Poll for session existence with exponential backoff (matching TmuxSession.Start pattern)
	log.InfoLog.Printf("Waiting for tmux session to be created...")
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !d.session.DoesSessionExist() {
		select {
		case <-timeout:
			ptmx.Close()
			log.ErrorLog.Printf("Timed out waiting for tmux session %s", fullSessionName)
			return fmt.Errorf("timed out waiting for tmux session %s", fullSessionName)
		default:
			time.Sleep(sleepDuration)
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}
	ptmx.Close()

	log.InfoLog.Printf("Session exists check: %v", d.session.DoesSessionExist())

	d.appendOutput(fmt.Sprintf("[%s] Starting dev server: %s", time.Now().Format("15:04:05"), d.config.DevCommand))

	return nil
}

var devCmd string

// devServerSessionName returns just the session name for tmux (without the prefix)
func devServerSessionName(instanceName string) string {
	whitespaceRegex := regexp.MustCompile(`\s+`)
	name := whitespaceRegex.ReplaceAllString(instanceName, "")
	name = strings.ReplaceAll(name, ".", "_")
	return fmt.Sprintf("%s_dev", name)
}
