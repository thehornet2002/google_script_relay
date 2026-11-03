package relay

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteSecureConfigFile writes data to targetPath atomically with restricted permissions (0600).
func AtomicWriteSecureConfigFile(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	tmpFile := targetPath + ".tmp"
	file, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open temp config file: %w", err)
	}

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to write temp config data: %w", err)
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to sync temp config data: %w", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to close temp config file: %w", err)
	}

	// Windows does not support atomic overwrite if destination exists, so backup/remove target first
	bakFile := targetPath + ".bak"
	if _, err := os.Stat(targetPath); err == nil {
		_ = os.Remove(bakFile)
		_ = os.Rename(targetPath, bakFile)
	}

	if err := os.Rename(tmpFile, targetPath); err != nil {
		// Attempt restore from backup if rename failed
		if _, statErr := os.Stat(bakFile); statErr == nil {
			_ = os.Rename(bakFile, targetPath)
		}
		return fmt.Errorf("failed to atomically replace config file: %w", err)
	}

	return nil
}
