package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
)

// NormalizeRoot returns an absolute, cleaned workspace root path.
func NormalizeRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("workspace root is required")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}

	return filepath.Clean(abs), nil
}
