//go:build !wasm

package pluginsdk

var mockState string

// GetState returns the current plugin state payload.
// In non-wasm builds it reads from an in-memory test stub.
func GetState() string {
	return mockState
}

// SetState persists the plugin state payload.
// In non-wasm builds it writes to an in-memory test stub.
func SetState(data string) error {
	mockState = data
	return nil
}
