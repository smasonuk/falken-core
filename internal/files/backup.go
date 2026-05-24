package files

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"
)

func (s *Service) backupExistingFile(path string) (string, error) {
	if s.backupRoot == "" {
		return "", ErrBackupRootRequired
	}

	if managedBackupBeforeReadHook != nil {
		managedBackupBeforeReadHook(path)
	}
	data, err := readManagedExistingFile(path, s.realWorkspaceRoot)
	if err != nil {
		return "", fmt.Errorf("read original file for backup: %w", err)
	}

	rel, err := filepath.Rel(s.realWorkspaceRoot, path)
	if err != nil {
		return "", fmt.Errorf("derive backup relative path: %w", err)
	}

	hash := sha256.Sum256(data)
	backupDir := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(hash[:4])
	backupPath := filepath.Join(s.backupRoot, "managed-writes", backupDir, rel)
	if err := writeWorkspaceFileAtomicMode(backupPath, data, 0o600, false, s.backupRoot); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}

	return backupPath, nil
}
