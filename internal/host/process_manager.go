package host

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type processManager struct{}

func (m *processManager) startProcess(s *StatefulShell, command string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()
	if s.dockerClient != nil && s.containerID != "" {
		logPath := filepath.ToSlash(filepath.Join("/tmp/falken-background", id+".log"))
		startCmd := fmt.Sprintf("mkdir -p /tmp/falken-background && : > %s && (%s) >> %s 2>&1 & echo $!", strconv.Quote(logPath), command, strconv.Quote(logPath))
		output, err := s.containerExecOutput(context.Background(), startCmd)
		if err != nil {
			return "", err
		}

		pid, err := strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			return "", fmt.Errorf("failed to parse sandbox pid from %q: %w", output, err)
		}

		s.Backgrounds[id] = &BackgroundProcess{
			Command: command,
			PID:     pid,
			LogPath: logPath,
		}
		return fmt.Sprintf("Started process %s with ID: %s", command, id), nil
	}

	if !s.TestingMode {
		return "", fmt.Errorf("FATAL: Sandbox is not active. Local host background execution is strictly disabled for security. Please restart the sandbox.")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = s.RealCWD
	cmd.Env = s.EnvVars

	buf := &bytes.Buffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return "", err
	}

	s.Backgrounds[id] = &BackgroundProcess{
		Command:  command,
		LocalCmd: cmd,
		LogBuf:   buf,
	}

	return fmt.Sprintf("Started process %s with ID: %s", command, id), nil
}

func (m *processManager) readProcessLogs(s *StatefulShell, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	process, ok := s.Backgrounds[id]
	if !ok {
		return "", fmt.Errorf("process not found")
	}
	if process.LogBuf != nil {
		return process.LogBuf.String(), nil
	}
	if process.LogPath == "" {
		return "", fmt.Errorf("process log path not found")
	}
	return s.containerExecOutput(context.Background(), fmt.Sprintf("cat %s", strconv.Quote(process.LogPath)))
}

func (m *processManager) killProcess(s *StatefulShell, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	process, ok := s.Backgrounds[id]
	if !ok {
		return "", fmt.Errorf("process not found")
	}

	err := m.killBackgroundProcessLocked(s, context.Background(), process)
	delete(s.Backgrounds, id)
	if err != nil {
		return "", err
	}
	return "Process killed successfully", nil
}

func (m *processManager) killBackgroundProcessLocked(s *StatefulShell, ctx context.Context, process *BackgroundProcess) error {
	if process == nil {
		return fmt.Errorf("process not found")
	}
	if process.LocalCmd != nil && process.LocalCmd.Process != nil {
		return process.LocalCmd.Process.Kill()
	}
	if process.PID == 0 {
		return fmt.Errorf("process pid not found")
	}
	_, err := s.containerExecOutput(ctx, fmt.Sprintf("kill %d", process.PID))
	return err
}

func (m *processManager) stopAllBackgroundProcesses(s *StatefulShell, ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, process := range s.Backgrounds {
		if err := m.killBackgroundProcessLocked(s, ctx, process); err != nil {
			s.Logger.Printf("Warning: Failed to stop background process %s: %v", id, err)
		}
		delete(s.Backgrounds, id)
	}
}
