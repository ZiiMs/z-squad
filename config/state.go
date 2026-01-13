package config

import (
	"claude-squad/log"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	StateFileName       = "state.json"
	InstancesFileName   = "instances.json"
	LegacyStateFileName = "state.json.legacy"
)

func repoIdentity(repoRoot string) string {
	hash := sha256.Sum256([]byte(repoRoot))
	return fmt.Sprintf("%x", hash)
}

// RepoIdentity is exported for use by other packages
func RepoIdentity(repoRoot string) string {
	return repoIdentity(repoRoot)
}

func getRepoStatePath(repoPath string) (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	identity := repoIdentity(repoPath)
	return filepath.Join(configDir, identity, StateFileName), nil
}

func getRepoWorktreesPath(repoPath string) (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	identity := repoIdentity(repoPath)
	return filepath.Join(configDir, identity, "worktrees"), nil
}

// InstanceStorage handles instance-related operations
type InstanceStorage interface {
	// SaveInstances saves the raw instance data
	SaveInstances(instancesJSON json.RawMessage) error
	// GetInstances returns the raw instance data
	GetInstances() json.RawMessage
	// DeleteAllInstances removes all stored instances
	DeleteAllInstances() error
}

// AppState handles application-level state
type AppState interface {
	// GetHelpScreensSeen returns the bitmask of seen help screens
	GetHelpScreensSeen() uint32
	// SetHelpScreensSeen updates the bitmask of seen help screens
	SetHelpScreensSeen(seen uint32) error
}

// StateManager combines instance storage and app state management
type StateManager interface {
	InstanceStorage
	AppState
}

// State represents the application state that persists between sessions
type State struct {
	// HelpScreensSeen is a bitmask tracking which help screens have been shown
	HelpScreensSeen uint32 `json:"help_screens_seen"`
	// Instances stores the serialized instance data as raw JSON
	InstancesData json.RawMessage `json:"instances"`
}

// DefaultState returns the default state
func DefaultState() *State {
	return &State{
		HelpScreensSeen: 0,
		InstancesData:   json.RawMessage("[]"),
	}
}

// LoadState loads the state from disk. If it cannot be done, we return the default state.
func LoadState() *State {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultState()
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default state if file doesn't exist
			defaultState := DefaultState()
			if saveErr := SaveState(defaultState); saveErr != nil {
				log.WarningLog.Printf("failed to save default state: %v", saveErr)
			}
			return defaultState
		}

		log.WarningLog.Printf("failed to get state file: %v", err)
		return DefaultState()
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		log.ErrorLog.Printf("failed to parse state file: %v", err)
		return DefaultState()
	}

	return &state
}

// SaveState saves the state to disk
func SaveState(state *State) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return os.WriteFile(statePath, data, 0644)
}

func LoadStateForRepo(repoPath string) *State {
	statePath, err := getRepoStatePath(repoPath)
	if err != nil {
		log.ErrorLog.Printf("failed to get repo state path: %v", err)
		return DefaultState()
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			defaultState := DefaultState()
			if saveErr := SaveStateForRepo(defaultState, repoPath); saveErr != nil {
				log.WarningLog.Printf("failed to save default state: %v", saveErr)
			}
			return defaultState
		}

		log.WarningLog.Printf("failed to read repo state file: %v", err)
		return DefaultState()
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		log.ErrorLog.Printf("failed to parse repo state file: %v", err)
		return DefaultState()
	}

	return &state
}

func SaveStateForRepo(state *State, repoPath string) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	identity := repoIdentity(repoPath)
	repoDir := filepath.Join(configDir, identity)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("failed to create repo directory: %w", err)
	}

	statePath := filepath.Join(repoDir, StateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return os.WriteFile(statePath, data, 0644)
}

func MigrateLegacyState() error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	legacyPath := filepath.Join(configDir, StateFileName)
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read legacy state file: %w", err)
	}

	var legacyState State
	if err := json.Unmarshal(data, &legacyState); err != nil {
		return fmt.Errorf("failed to parse legacy state file: %w", err)
	}

	var instancesData []map[string]interface{}
	if err := json.Unmarshal(legacyState.InstancesData, &instancesData); err != nil {
		instancesData = []map[string]interface{}{}
	}

	repoGroups := make(map[string][]map[string]interface{})
	for _, inst := range instancesData {
		worktree, ok := inst["worktree"].(map[string]interface{})
		if !ok {
			continue
		}
		repoPath, ok := worktree["repo_path"].(string)
		if !ok || repoPath == "" {
			continue
		}
		repoGroups[repoPath] = append(repoGroups[repoPath], inst)
	}

	for repoPath, instances := range repoGroups {
		identity := repoIdentity(repoPath)
		repoDir := filepath.Join(configDir, identity)

		if err := os.MkdirAll(repoDir, 0755); err != nil {
			log.ErrorLog.Printf("failed to create repo directory for %s: %v", repoPath, err)
			continue
		}

		instancesJSON, err := json.Marshal(instances)
		if err != nil {
			log.ErrorLog.Printf("failed to marshal instances for %s: %v", repoPath, err)
			continue
		}

		state := &State{
			HelpScreensSeen: legacyState.HelpScreensSeen,
			InstancesData:   instancesJSON,
		}

		statePath := filepath.Join(repoDir, StateFileName)
		stateData, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			log.ErrorLog.Printf("failed to marshal state for %s: %v", repoPath, err)
			continue
		}

		if err := os.WriteFile(statePath, stateData, 0644); err != nil {
			log.ErrorLog.Printf("failed to write state for %s: %v", repoPath, err)
			continue
		}

		log.InfoLog.Printf("migrated %d instances for repo %s", len(instances), repoPath)
	}

	backupPath := filepath.Join(configDir, LegacyStateFileName)
	if err := os.Rename(legacyPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup legacy state: %w", err)
	}

	log.InfoLog.Printf("legacy state migrated to %s", LegacyStateFileName)
	return nil
}

func NeedsMigration() bool {
	configDir, err := GetConfigDir()
	if err != nil {
		return false
	}
	legacyPath := filepath.Join(configDir, StateFileName)
	_, err = os.Stat(legacyPath)
	return err == nil
}

// InstanceStorage interface implementation

// SaveInstances saves the raw instance data
func (s *State) SaveInstances(instancesJSON json.RawMessage) error {
	s.InstancesData = instancesJSON
	return SaveState(s)
}

// GetInstances returns the raw instance data
func (s *State) GetInstances() json.RawMessage {
	return s.InstancesData
}

// DeleteAllInstances removes all stored instances
func (s *State) DeleteAllInstances() error {
	s.InstancesData = json.RawMessage("[]")
	return SaveState(s)
}

// AppState interface implementation

// GetHelpScreensSeen returns the bitmask of seen help screens
func (s *State) GetHelpScreensSeen() uint32 {
	return s.HelpScreensSeen
}

// SetHelpScreensSeen updates the bitmask of seen help screens
func (s *State) SetHelpScreensSeen(seen uint32) error {
	s.HelpScreensSeen = seen
	return SaveState(s)
}
