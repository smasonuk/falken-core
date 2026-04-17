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

//go:wasmimport env host_start_process
func host_start_process(cmdPtr, cmdLen uint32) uint32

//go:wasmimport env host_read_process_logs
func host_read_process_logs(idPtr, idLen uint32) uint32

//go:wasmimport env host_kill_process
func host_kill_process(idPtr, idLen uint32) uint32

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
	case "start_background_process":
		result = handleStartProcess(payload.Args)
	case "read_process_logs":
		result = handleReadLogs(payload.Args)
	case "kill_process":
		result = handleKillProcess(payload.Args)
	default:
		result = fmt.Sprintf("error: unknown tool command '%s'", payload.Command)
	}

	outBytes, _ := json.Marshal(map[string]string{"result": result})
	fmt.Print(string(outBytes))
}

func handleStartProcess(args map[string]any) string {
	cmd, _ := args["Command"].(string)
	if cmd == "" {
		cmd, _ = args["command"].(string)
	}
	if cmd == "" {
		return "error: Command parameter is required"
	}

	cmdBytes := []byte(cmd)
	cmdPtr := uint32(uintptr(unsafe.Pointer(&cmdBytes[0])))

	resPtr := host_start_process(cmdPtr, uint32(len(cmdBytes)))
	return pluginsdk.PtrToString(resPtr)
}

func handleReadLogs(args map[string]any) string {
	id, _ := args["ID"].(string)
	if id == "" {
		id, _ = args["id"].(string)
	}
	if id == "" {
		return "error: ID parameter is required"
	}

	idBytes := []byte(id)
	idPtr := uint32(uintptr(unsafe.Pointer(&idBytes[0])))

	resPtr := host_read_process_logs(idPtr, uint32(len(idBytes)))
	return pluginsdk.PtrToString(resPtr)
}

func handleKillProcess(args map[string]any) string {
	id, _ := args["ID"].(string)
	if id == "" {
		id, _ = args["id"].(string)
	}
	if id == "" {
		return "error: ID parameter is required"
	}

	idBytes := []byte(id)
	idPtr := uint32(uintptr(unsafe.Pointer(&idBytes[0])))

	resPtr := host_kill_process(idPtr, uint32(len(idBytes)))
	return pluginsdk.PtrToString(resPtr)
}
