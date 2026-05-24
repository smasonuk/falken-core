//go:build windows

package files

import (
	"fmt"
	"os"
)

func managedFileServiceSupported() bool {
	return false
}

func fileIdentity(os.FileInfo) (uint64, uint64) {
	return 0, 0
}

func writeWorkspaceFileAtomicModeWithValidation(string, []byte, os.FileMode, bool, func() error, ...string) error {
	return ErrUnsupportedPlatform
}

func readManagedExistingFile(string, string) ([]byte, error) {
	return nil, ErrUnsupportedPlatform
}

func readManagedExistingFileSnapshot(string, string) ([]byte, os.FileInfo, error) {
	return nil, nil, ErrUnsupportedPlatform
}

func deleteManagedExistingFile(string, string) error {
	return ErrUnsupportedPlatform
}

func validateManagedRegularFile(*os.File, string) error {
	return ErrUnsupportedPlatform
}

func syncParentFile(_ *os.File, path string) error {
	return fmt.Errorf("%w: sync parent directory for %q", ErrUnsupportedPlatform, path)
}
