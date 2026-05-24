package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadText reads a text file. The returned boolean reports whether the file existed.
func ReadText(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("read text file %q: %w", path, err)
	}

	return string(data), true, nil
}

// WriteTextAtomic atomically writes text content to path, creating parent directories as needed.
// It is intended for trusted Falken state/artifact paths, not policy-managed
// workspace mutations. Workspace writes should go through internal/files.Service
// so policy, read tokens, backups, and symlink-safe path handling are enforced.
func WriteTextAtomic(path, content string, perm os.FileMode) error {
	return writeFileAtomic(path, []byte(content), perm)
}

// WriteBytesAtomic atomically writes bytes to path, creating parent directories as needed.
// Like WriteTextAtomic, it is for trusted state/artifact paths. Do not use it
// for workspace files that need managed mutation policy.
func WriteBytesAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, append([]byte(nil), data...), perm)
}

// ReadJSON reads JSON into target. The returned boolean reports whether the file existed.
func ReadJSON(path string, target any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("read JSON file %q: %w", path, err)
	}

	if err := json.Unmarshal(data, target); err != nil {
		return false, fmt.Errorf("decode JSON file %q: %w", path, err)
	}

	return true, nil
}

// WriteJSONAtomic atomically writes JSON content to path, creating parent directories as needed.
// It shares the same trusted-state scope as WriteTextAtomic.
func WriteJSONAtomic(path string, value any, perm os.FileMode) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON file %q: %w", path, err)
	}

	payload = append(payload, '\n')

	if err := writeFileAtomic(path, payload, perm); err != nil {
		return fmt.Errorf("write JSON file %q: %w", path, err)
	}

	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", path, err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".persist-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("set permissions on temporary file for %q: %w", path, err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temporary file for %q: %w", path, err)
	}

	cleanup = false
	if err := syncParentDir(path); err != nil {
		return err
	}
	return nil
}

func syncParentDir(path string) error {
	dir := filepath.Dir(path)
	dirFD, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory for sync %q: %w", dir, err)
	}
	defer func() { _ = dirFD.Close() }()
	if err := dirFD.Sync(); err != nil {
		return fmt.Errorf("sync parent directory for %q: %w", path, err)
	}
	return nil
}
