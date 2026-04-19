//go:build wasm

package pluginsdk

import (
	"fmt"
	"unsafe"
)

//go:wasmimport env host_get_state
func host_get_state() uint32

//go:wasmimport env host_set_state
func host_set_state(ptr, len uint32) uint32

// GetState asks the host for the persistent state for this plugin.
func GetState() string {
	resPtr := host_get_state()
	return PtrToString(resPtr)
}

// SetState asks the host to securely persist state data for this plugin.
func SetState(data string) error {
	dataBytes := []byte(data)
	if len(dataBytes) == 0 {
		return nil
	}
	ptr := uint32(uintptr(unsafe.Pointer(&dataBytes[0])))

	resPtr := host_set_state(ptr, uint32(len(dataBytes)))
	resStr := PtrToString(resPtr)

	if resStr != "success" {
		return fmt.Errorf("host failed to save state: %s", resStr)
	}
	return nil
}
