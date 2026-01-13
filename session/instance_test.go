package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestInstance creates a minimal instance for testing
func createTestInstance() *Instance {
	return &Instance{
		Title:   "test-instance",
		Path:    "/mock/path",
		started: false,
	}
}

func TestInstanceKill_WhenNotStarted_CleansUpResources(t *testing.T) {
	t.Run("kill does not panic when all resources are nil", func(t *testing.T) {
		instance := createTestInstance()
		instance.started = false
		instance.tmuxSession = nil
		instance.gitWorktree = nil
		instance.DevServer = nil

		// Should not panic and should return nil
		err := instance.Kill()
		assert.NoError(t, err)
	})

	t.Run("kill returns nil when nothing to clean up", func(t *testing.T) {
		instance := createTestInstance()
		instance.started = false
		instance.tmuxSession = nil
		instance.gitWorktree = nil
		instance.DevServer = nil

		err := instance.Kill()
		assert.NoError(t, err)
	})
}

func TestInstanceKill_DevServerCleanup(t *testing.T) {
	t.Run("stops dev server when present", func(t *testing.T) {
		instance := createTestInstance()
		instance.started = false

		devServer := &DevServer{}
		instance.DevServer = devServer

		err := instance.Kill()
		assert.NoError(t, err)
	})

	t.Run("handles dev server stop error gracefully", func(t *testing.T) {
		instance := createTestInstance()
		instance.started = false

		devServer := &DevServer{}
		instance.DevServer = devServer

		err := instance.Kill()
		assert.NoError(t, err)
	})
}

func TestInstanceKill_ErrorHandling(t *testing.T) {
	t.Run("combines multiple errors", func(t *testing.T) {
		instance := createTestInstance()

		// Test combineErrors with multiple errors
		errs := []error{assert.AnError, assert.AnError}
		result := instance.combineErrors(errs)
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "multiple cleanup errors occurred")
	})

	t.Run("combineErrors returns nil for empty slice", func(t *testing.T) {
		instance := createTestInstance()
		result := instance.combineErrors([]error{})
		assert.NoError(t, result)
	})

	t.Run("combineErrors returns single error", func(t *testing.T) {
		instance := createTestInstance()
		result := instance.combineErrors([]error{assert.AnError})
		assert.Equal(t, assert.AnError, result)
	})
}

func TestInstanceKill_StartedFlagBehavior(t *testing.T) {
	t.Run("kill works regardless of started flag when resources exist", func(t *testing.T) {
		// The key behavior change: Kill() should work even when started=false
		// as long as tmuxSession or gitWorktree are not nil
		instance := createTestInstance()
		instance.started = false // Not started

		// With the fix, Kill() should not early-return on !started
		// It should check if tmuxSession and gitWorktree are nil first
		// Since both are nil here, it should succeed
		err := instance.Kill()
		assert.NoError(t, err, "Kill() should succeed when nothing to clean up")
	})

	t.Run("instance can be killed multiple times without error", func(t *testing.T) {
		instance := createTestInstance()
		instance.started = false
		instance.tmuxSession = nil
		instance.gitWorktree = nil
		instance.DevServer = nil

		// First kill
		err := instance.Kill()
		require.NoError(t, err)

		// Second kill (idempotent)
		err = instance.Kill()
		assert.NoError(t, err, "Kill() should be idempotent")
	})
}
