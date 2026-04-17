package main

import (
	"os"
	"path/filepath"
)

const defaultNewFileMode = 0644

func determineWriteMode(path string, fallback os.FileMode) os.FileMode {
	if fallback == 0 {
		fallback = defaultNewFileMode
	}
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode()
	}
	return fallback
}

func writeFilePreservingMode(path string, content []byte, fallback os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	mode := determineWriteMode(path, fallback)
	return os.WriteFile(path, content, mode)
}
