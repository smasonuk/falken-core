package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

type sandboxManager struct{}

func (m *sandboxManager) start(s *StatefulShell, ctx context.Context, imageName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	s.dockerClient = cli

	sessionID := uuid.New().String()
	blockedFiles := []string{}
	if s.PermManager != nil && s.PermManager.Config != nil {
		blockedFiles = s.PermManager.Config.GlobalBlockedFiles
	}
	sandboxDir, err := CreateSnapshot(s.RealCWD, sessionID, blockedFiles)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}
	s.SandboxCWD = sandboxDir

	currentUser, err := user.Current()
	if err == nil {
		s.mappedUser = fmt.Sprintf("%s:%s", currentUser.Uid, currentUser.Gid)
	}

	proxyPort := s.ProxyPort
	if proxyPort == "" {
		proxyPort = "8080"
	}
	proxyURL := "http://host.docker.internal:" + proxyPort
	envVars := []string{
		"HTTP_PROXY=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL,
		"NO_PROXY=localhost,127.0.0.1",
	}

	paths := runtimeapi.Paths{
		WorkspaceDir: s.WorkspaceDir,
		StateDir:     s.StateDir,
	}
	hostCertPath, _ := filepath.Abs(paths.ProxyCertPath())
	binds := []string{
		fmt.Sprintf("%s:%s", s.SandboxCWD, s.RealCWD),
		fmt.Sprintf("%s:/usr/local/share/ca-certificates/falken-proxy.crt:ro", hostCertPath),
	}

	if s.PermManager != nil && s.PermManager.Config != nil {
		cachesDir := paths.MountedCachesDir()
		os.MkdirAll(cachesDir, 0755)

		for cacheName, cacheCfg := range s.PermManager.Config.Caches {
			hostCachePath := filepath.Join(cachesDir, cacheName)
			os.MkdirAll(hostCachePath, 0755)
			binds = append(binds, fmt.Sprintf("%s:%s", hostCachePath, cacheCfg.ContainerPath))
		}
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:      imageName,
		Cmd:        []string{"bash", "-c", "update-ca-certificates && cat"},
		WorkingDir: s.RealCWD,
		User:       "root",
		Env:        envVars,
		OpenStdin:  true,
		StdinOnce:  true,
	}, &container.HostConfig{
		AutoRemove: true,
		Binds:      binds,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}, nil, nil, "")
	if err != nil {
		return err
	}

	s.containerID = resp.ID
	s.Logger.Printf("Container created: %s", s.containerID)

	if err := cli.ContainerStart(ctx, s.containerID, container.StartOptions{}); err != nil {
		return err
	}

	inspect, err := cli.ContainerInspect(ctx, s.containerID)
	if err == nil {
		s.ContainerEnv = inspect.Config.Env
	}

	attach, err := cli.ContainerAttach(ctx, s.containerID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
	})
	if err == nil {
		go func() {
			<-ctx.Done()
			attach.Close()
		}()
	} else {
		s.Logger.Printf("Warning: Failed to tether container stdin: %v", err)
	}

	return nil
}

func (m *sandboxManager) close(s *StatefulShell, ctx context.Context) error {
	s.stopAllBackgroundProcesses(ctx)

	if s.dockerClient != nil && s.containerID != "" {
		_ = s.dockerClient.ContainerStop(ctx, s.containerID, container.StopOptions{})
		if err := s.dockerClient.ContainerRemove(ctx, s.containerID, container.RemoveOptions{Force: true}); err != nil {
			msg := err.Error()
			if !strings.Contains(msg, "No such container") && !strings.Contains(msg, "not found") {
				s.Logger.Printf("Warning: Failed to remove container: %v", err)
			}
		}
	}

	if s.SandboxCWD != "" {
		os.RemoveAll(s.SandboxCWD)
	}

	return nil
}

func (m *sandboxManager) containerWorkingDir(s *StatefulShell) string {
	workingDir := s.RealCWD
	if s.SandboxCWD != "" && strings.HasPrefix(s.RealCWD, s.SandboxCWD) {
		rel, _ := filepath.Rel(s.SandboxCWD, s.RealCWD)
		if s.WorkspaceDir != "" {
			workingDir = filepath.Join(s.WorkspaceDir, rel)
		} else {
			workingDir = filepath.Join("/", rel)
		}
	}
	return workingDir
}

func (m *sandboxManager) containerExecEnv(s *StatefulShell) []string {
	execEnv := append([]string{}, s.ContainerEnv...)
	execEnv = append(execEnv, s.EnvVars...)

	// TODO: should add these to the config file and not hard coded
	execEnv = append(execEnv,
		"TMPDIR=/tmp",
		"GOTMPDIR=/tmp",
		"HOME=/tmp",
		"GOFLAGS=-buildvcs=false",
	)

	if s.PermManager != nil && s.PermManager.Config != nil {
		for cacheName, cacheCfg := range s.PermManager.Config.Caches {
			hasPath := false
			for _, envVar := range cacheCfg.Env {
				if strings.HasPrefix(envVar, "PATH=") {
					hasPath = true
				}
			}
			if !hasPath {
				execEnv = append(execEnv, cacheCfg.Env...)
			} else {
				s.Logger.Printf("Warning: Dropped PATH override from cache config %s for security.", cacheName)
			}
		}
	}

	return execEnv
}

func (m *sandboxManager) containerExecOutput(s *StatefulShell, ctx context.Context, command string) (string, error) {
	if s.dockerClient == nil || s.containerID == "" {
		return "", fmt.Errorf("sandbox is not active")
	}

	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-lc", command},
		WorkingDir:   m.containerWorkingDir(s),
		Env:          m.containerExecEnv(s),
		User:         s.mappedUser,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
	if err != nil {
		return "", err
	}

	resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{Tty: false})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, resp.Reader); err != nil {
		return "", err
	}

	output := outBuf.String()
	if errBuf.Len() > 0 {
		output += errBuf.String()
	}

	inspect, err := s.dockerClient.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return output, err
	}
	if inspect.ExitCode != 0 {
		return output, fmt.Errorf("exit code %d", inspect.ExitCode)
	}

	return output, nil
}
