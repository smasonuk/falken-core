//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/smasonuk/falken-core/internal/workspace"
	"golang.org/x/sys/unix"
)

func managedFileServiceSupported() bool {
	return true
}

func fileIdentity(stat os.FileInfo) (uint64, uint64) {
	if stat == nil {
		return 0, 0
	}
	if sys, ok := stat.Sys().(*unix.Stat_t); ok && sys != nil {
		return uint64(sys.Dev), uint64(sys.Ino)
	}
	return 0, 0
}

func writeWorkspaceFileAtomicModeWithValidation(path string, data []byte, perm os.FileMode, noReplace bool, beforeCommit func() error, trustedRoots ...string) error {
	trustedRoot, normalizedPath, err := requiredTrustedWriteRoot(path, trustedRoots...)
	if err != nil {
		return err
	}
	path = normalizedPath
	parent, targetName, err := openManagedWriteParent(path, trustedRoot)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()

	if managedWriteBeforeCreateTempHook != nil {
		managedWriteBeforeCreateTempHook(path)
	}
	tmpName, tmpFile, err := createManagedTempFile(parent, path, perm)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Unlinkat(int(parent.Fd()), tmpName, 0)
		}
	}()

	if err := tmpFile.Chmod(perm.Perm()); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("set permissions on temporary file for %q: %w", path, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if managedWriteBeforeCommitHook != nil {
		managedWriteBeforeCommitHook(path)
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	parentFd := int(parent.Fd())
	if noReplace {
		if err := unix.Linkat(parentFd, tmpName, parentFd, targetName, 0); err != nil {
			if errors.Is(err, os.ErrExist) {
				return errWorkspaceFileAlreadyExists
			}
			return fmt.Errorf("link temporary file for %q: %w", path, err)
		}
		if err := unix.Unlinkat(parentFd, tmpName, 0); err != nil {
			return mutationMayHaveOccurredError{operation: "unlink temporary file for " + path, err: err}
		}
		cleanup = false
		if err := syncManagedParentDir(parent, path); err != nil {
			return mutationMayHaveOccurredError{operation: "link temporary file for " + path, err: err}
		}
		return nil
	}
	if err := validateManagedOverwriteTarget(parentFd, targetName, path); err != nil {
		return err
	}
	if err := unix.Renameat(parentFd, tmpName, parentFd, targetName); err != nil {
		return fmt.Errorf("rename temporary file for %q: %w", path, err)
	}

	cleanup = false
	if err := syncManagedParentDir(parent, path); err != nil {
		return mutationMayHaveOccurredError{operation: "rename temporary file for " + path, err: err}
	}
	return nil
}

func openManagedWriteParent(path, trustedRoot string) (*os.File, string, error) {
	path = filepath.Clean(path)
	targetName := filepath.Base(path)
	if targetName == "." || targetName == string(filepath.Separator) || targetName == "" {
		return nil, "", fmt.Errorf("invalid write target %q", path)
	}
	root := filepath.Clean(trustedRoot)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, "", fmt.Errorf("derive write target relative path: %w", err)
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("%w: write target %q escaped trusted root", workspace.ErrPathOutsideWorkspace, path)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	parentParts := parts[:len(parts)-1]

	return openManagedParentParts(root, path, parentParts, true, targetName)
}

func openManagedExistingParent(path, trustedRoot string) (*os.File, string, error) {
	trustedRoot, normalizedPath, err := requiredTrustedWriteRoot(path, trustedRoot)
	if err != nil {
		return nil, "", err
	}
	path = normalizedPath
	targetName := filepath.Base(path)
	if targetName == "." || targetName == string(filepath.Separator) || targetName == "" {
		return nil, "", fmt.Errorf("invalid managed file target %q", path)
	}
	rel, err := filepath.Rel(trustedRoot, path)
	if err != nil {
		return nil, "", fmt.Errorf("derive managed file relative path: %w", err)
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("%w: managed file target %q escaped trusted root", workspace.ErrPathOutsideWorkspace, path)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return openManagedParentParts(trustedRoot, path, parts[:len(parts)-1], false, targetName)
}

func openManagedParentParts(root, path string, parentParts []string, createParents bool, targetName string) (*os.File, string, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open trusted write root %q: %w", root, err)
	}
	for _, part := range parentParts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			_ = unix.Close(fd)
			return nil, "", fmt.Errorf("%w: write parent %q escaped trusted root", workspace.ErrPathOutsideWorkspace, path)
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if createParents && err != nil && errors.Is(err, os.ErrNotExist) {
			if mkdirErr := unix.Mkdirat(fd, part, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				_ = unix.Close(fd)
				return nil, "", fmt.Errorf("create parent directory %q for %q: %w", part, path, mkdirErr)
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			_ = unix.Close(fd)
			return nil, "", fmt.Errorf("open parent directory %q for %q: %w", part, path, err)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), targetName, nil
}

func requiredTrustedWriteRoot(path string, trustedRoots ...string) (string, string, error) {
	if len(trustedRoots) == 0 || strings.TrimSpace(trustedRoots[0]) == "" {
		return "", "", fmt.Errorf("trusted write root is required for %q", path)
	}
	lexicalRoot, err := filepath.Abs(trustedRoots[0])
	if err != nil {
		return "", "", fmt.Errorf("resolve trusted write root: %w", err)
	}
	lexicalRoot = filepath.Clean(lexicalRoot)
	realRoot, err := filepath.EvalSymlinks(lexicalRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve trusted write root symlinks: %w", err)
	}
	realRoot = filepath.Clean(realRoot)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve write target: %w", err)
	}
	absPath = filepath.Clean(absPath)
	if rel, ok := trustedWriteRel(lexicalRoot, absPath); ok {
		return realRoot, filepath.Join(realRoot, rel), nil
	}
	if rel, ok := trustedWriteRel(realRoot, absPath); ok {
		return realRoot, filepath.Join(realRoot, rel), nil
	}
	return "", "", fmt.Errorf("%w: write target %q escaped trusted root", workspace.ErrPathOutsideWorkspace, path)
}

func trustedWriteRel(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func createManagedTempFile(parent *os.File, path string, perm os.FileMode) (string, *os.File, error) {
	parentFd := int(parent.Fd())
	for i := 0; i < 100; i++ {
		name, err := managedTempName()
		if err != nil {
			return "", nil, err
		}
		fd, err := unix.Openat(parentFd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
		if err == nil {
			return name, os.NewFile(uintptr(fd), name), nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return "", nil, fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	return "", nil, fmt.Errorf("create temporary file for %q: exhausted unique names", path)
}

func readManagedExistingFile(path, trustedRoot string) ([]byte, error) {
	data, _, err := readManagedExistingFileSnapshot(path, trustedRoot)
	return data, err
}

func readManagedExistingFileSnapshot(path, trustedRoot string) ([]byte, os.FileInfo, error) {
	parent, targetName, err := openManagedExistingParent(path, trustedRoot)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = parent.Close() }()

	fd, err := unix.Openat(int(parent.Fd()), targetName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open managed file %q: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), targetName)
	defer func() { _ = file.Close() }()
	stat, err := managedRegularFileInfo(file, path)
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, fmt.Errorf("read managed file %q: %w", path, err)
	}
	return data, stat, nil
}

func deleteManagedExistingFile(path, trustedRoot string) error {
	parent, targetName, err := openManagedExistingParent(path, trustedRoot)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()

	if err := validateManagedRegularDirEntry(int(parent.Fd()), targetName, path); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(parent.Fd()), targetName, 0); err != nil {
		return fmt.Errorf("delete managed file %q: %w", path, err)
	}
	if err := syncManagedParentDir(parent, path); err != nil {
		return mutationMayHaveOccurredError{operation: "delete managed file for " + path, err: err}
	}
	return nil
}

func validateManagedRegularDirEntry(parentFd int, targetName, path string) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFd, targetName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat managed file %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("managed file %q is not a regular file", path)
	}
	return nil
}

func validateManagedOverwriteTarget(parentFd int, targetName, path string) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFd, targetName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat overwrite target %q: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("overwrite target %q is not a regular file", path)
	}
	return nil
}

func validateManagedRegularFile(file *os.File, path string) error {
	_, err := managedRegularFileInfo(file, path)
	return err
}

func managedRegularFileInfo(file *os.File, path string) (os.FileInfo, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat managed file %q: %w", path, err)
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("managed file %q is not a regular file", path)
	}
	return stat, nil
}

func managedTempName() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate temporary file name: %w", err)
	}
	return ".falken-write-" + hex.EncodeToString(nonce[:]) + ".tmp", nil
}

func syncParentFile(parent *os.File, path string) error {
	if parent == nil {
		return fmt.Errorf("sync parent directory for %q: parent directory handle is unavailable", path)
	}
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync parent directory for %q: %w", path, err)
	}
	return nil
}
