package pluginsdk

import (
	"unsafe"
)

// PtrToString reads a null-terminated string from wasm memory.
func PtrToString(ptr uint32) string {
	if ptr == 0 {
		return ""
	}

	var bytes []byte
	currentPtr := uintptr(ptr)
	for {
		b := *(*byte)(unsafe.Pointer(currentPtr))
		if b == 0 {
			break
		}
		bytes = append(bytes, b)
		currentPtr++
	}

	return string(bytes)
}
