package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const SettingsFileName = ".claude-squad/settings.json"

type DevServerSettings struct {
	BuildCommand string            `json:"build_command"`
	DevCommand   string            `json:"dev_command"`
	Env          map[string]string `json:"env,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

func DefaultDevServerSettings() *DevServerSettings {
	return &DevServerSettings{
		BuildCommand: "",
		DevCommand:   "",
		Env:          make(map[string]string),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func LoadDevServerSettings(repoPath string) (*DevServerSettings, error) {
	settingsPath := filepath.Join(repoPath, SettingsFileName)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}

	var settings DevServerSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings file: %w", err)
	}

	return &settings, nil
}

func SaveDevServerSettings(settings *DevServerSettings, repoPath string) error {
	settings.UpdatedAt = time.Now()

	settingsDir := filepath.Join(repoPath, ".claude-squad")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	settingsPath := filepath.Join(repoPath, SettingsFileName)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	return os.WriteFile(settingsPath, data, 0644)
}

func CopySettingsToWorktree(mainRepoPath, worktreePath string) error {
	mainSettingsPath := filepath.Join(mainRepoPath, SettingsFileName)
	worktreeSettingsPath := filepath.Join(worktreePath, SettingsFileName)

	data, err := os.ReadFile(mainSettingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read main settings file: %w", err)
	}

	worktreeSettingsDir := filepath.Join(worktreePath, ".claude-squad")
	if err := os.MkdirAll(worktreeSettingsDir, 0755); err != nil {
		return fmt.Errorf("failed to create worktree settings directory: %w", err)
	}

	return os.WriteFile(worktreeSettingsPath, data, 0644)
}

func CopyEnvFiles(mainRepoPath, worktreePath string) error {
	entries, err := os.ReadDir(mainRepoPath)
	if err != nil {
		return fmt.Errorf("failed to read main repo directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == ".env" || (len(name) > 5 && name[:5] == ".env.") {
			srcPath := filepath.Join(mainRepoPath, name)
			dstPath := filepath.Join(worktreePath, name)
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read env file %s: %w", name, err)
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write env file %s: %w", name, err)
			}
		}
	}
	return nil
}

func SettingsExist(repoPath string) bool {
	settingsPath := filepath.Join(repoPath, SettingsFileName)
	_, err := os.Stat(settingsPath)
	return err == nil
}
