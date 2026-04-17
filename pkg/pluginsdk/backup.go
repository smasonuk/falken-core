//go:build !wasm

package pluginsdk

// BackupFile asks the host to preserve original file contents before destructive edits.
// In non-wasm builds it is a no-op helper for tests and local tooling.
func BackupFile(originalPath string, content []byte) error {
	return nil
}
