package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/smasonuk/falken-core/pkg/pluginsdk"
)

type FileState struct {
	LastRead time.Time `json:"last_read"`
}

func markFileAsRead(path string) {
	stateRaw := pluginsdk.GetState()
	state := make(map[string]FileState)

	if stateRaw != "" {
		json.Unmarshal([]byte(stateRaw), &state)
	}

	info, err := os.Stat(path)
	if err == nil {
		state[path] = FileState{LastRead: info.ModTime()}
		newData, _ := json.Marshal(state)
		pluginsdk.SetState(string(newData))
	}
}

func verifyFileRead(path string) error {
	stateRaw := pluginsdk.GetState()
	if stateRaw == "" {
		return fmt.Errorf("SECURITY/CONTEXT GUARD: You must read this file using the 'read_file' tool before you are allowed to edit it.")
	}

	state := make(map[string]FileState)
	if err := json.Unmarshal([]byte(stateRaw), &state); err != nil {
		return fmt.Errorf("internal error: corrupted plugin state")
	}

	fileState, ok := state[path]
	if !ok {
		return fmt.Errorf("SECURITY/CONTEXT GUARD: You must read this file using the 'read_file' tool before you are allowed to edit it.")
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.ModTime().After(fileState.LastRead) {
		return fmt.Errorf("RACE CONDITION GUARD: File has been modified by an external process since you last read it. Please read it again before attempting an edit.")
	}

	return nil
}
