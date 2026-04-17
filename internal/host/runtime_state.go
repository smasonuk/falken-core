package host

import (
	"os"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

func ResetRuntimeState(paths runtimeapi.Paths) error {
	if err := paths.EnsureStateDirs(); err != nil {
		return err
	}
	if err := removeAndRecreate(paths.BackupDir()); err != nil {
		return err
	}
	if err := removeAndRecreate(paths.TruncationDir()); err != nil {
		return err
	}
	return nil
}

func removeAndRecreate(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0755)
}
