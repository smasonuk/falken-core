//go:build wasm

package pluginsdk

import (
	"fmt"
	"unsafe"
)

//go:wasmimport env host_backup_file
func host_backup_file(pathPtr, pathLen, contentPtr, contentLen uint32) uint32

// BackupFile asks the host to securely back up original file contents before mutation.
func BackupFile(originalPath string, content []byte) error {
	pathBytes := []byte(originalPath)
	if len(pathBytes) == 0 {
		return fmt.Errorf("invalid path provided for backup")
	}
	pathPtr := uint32(uintptr(unsafe.Pointer(&pathBytes[0])))

	var contentPtr uint32
	if len(content) > 0 {
		contentPtr = uint32(uintptr(unsafe.Pointer(&content[0])))
	}

	resPtr := host_backup_file(pathPtr, uint32(len(pathBytes)), contentPtr, uint32(len(content)))
	resStr := PtrToString(resPtr)

	if resStr != "success" {
		return fmt.Errorf("host failed to backup file: %s", resStr)
	}
	return nil
}
