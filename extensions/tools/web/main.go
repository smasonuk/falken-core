//go:build wasm

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

//go:wasmimport env host_fetch_url
func host_fetch_url(urlPtr, urlLen uint32) uint32

type PluginPayload struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
}

var keepAlive []byte

//export alloc_mem
func alloc_mem(size uint32) uint32 {
	keepAlive = make([]byte, size+1)
	return uint32(uintptr(unsafe.Pointer(&keepAlive[0])))
}

func main() {
	inputBytes, _ := io.ReadAll(os.Stdin)

	var payload PluginPayload
	if err := json.Unmarshal(inputBytes, &payload); err != nil {
		fmt.Printf(`{"result": "error: failed to parse JSON payload"}` + "\n")
		return
	}

	var result string
	switch payload.Command {
	case "fetch_url":
		result = handleFetchURL(payload.Args)
	default:
		result = fmt.Sprintf("error: unknown tool command '%s'", payload.Command)
	}

	outBytes, _ := json.Marshal(map[string]string{"result": result})
	fmt.Print(string(outBytes))
}

func handleFetchURL(args map[string]any) string {
	url, _ := args["URL"].(string)
	if url == "" {
		url, _ = args["url"].(string)
	}
	if url == "" {
		return "error: URL parameter is required"
	}

	urlBytes := []byte(url)
	urlPtr := uint32(uintptr(unsafe.Pointer(&urlBytes[0])))

	resPtr := host_fetch_url(urlPtr, uint32(len(urlBytes)))
	return pluginsdk.PtrToString(resPtr)
}
