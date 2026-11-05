//go:build linux

package session

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// createSpillFile creates dir/name with content without following symlinks in
// any component of dir, closing the RT-07 check-use window: the directory is
// opened component by component with O_NOFOLLOW|O_DIRECTORY, the file is
// created through that descriptor with O_CREAT|O_EXCL|O_NOFOLLOW, and the
// directory identity is re-checked afterwards. A symlink swapped in after
// validation (the deterministic test hook) therefore makes the openat fail or
// the identity check fail; in both cases the file is removed through the open
// descriptor and the caller keeps the full result.
func createSpillFile(dir, name, content string) error {
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("tool-result spill: directory %q is not absolute", dir)
	}
	dirFd, err := openDirNoFollow(dir)
	if err != nil {
		return err
	}
	defer syscall.Close(dirFd) //nolint:errcheck // read-only descriptor

	fileFd, err := syscall.Openat(dirFd, name,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("tool-result spill: create %s/%s: %w", dir, name, err)
	}
	file := os.NewFile(uintptr(fileFd), name)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = syscall.Unlinkat(dirFd, name)
		return fmt.Errorf("tool-result spill: write %s/%s: %w", dir, name, err)
	}
	if err := file.Close(); err != nil {
		_ = syscall.Unlinkat(dirFd, name)
		return fmt.Errorf("tool-result spill: close %s/%s: %w", dir, name, err)
	}

	// The path that was validated must still name the directory we wrote into.
	// If an attacker swapped it for a symlink, the file just written lives in a
	// detached directory: remove it and refuse the spill.
	var opened, current syscall.Stat_t
	if err := syscall.Fstat(dirFd, &opened); err != nil {
		_ = syscall.Unlinkat(dirFd, name)
		return fmt.Errorf("tool-result spill: fstat verified directory: %w", err)
	}
	if err := syscall.Lstat(dir, &current); err != nil || opened.Dev != current.Dev || opened.Ino != current.Ino {
		_ = syscall.Unlinkat(dirFd, name)
		return fmt.Errorf("tool-result spill: directory %q changed while writing; refusing the spill", dir)
	}
	return nil
}

// openDirNoFollow opens an absolute directory path without following symlinks in
// any component.
func openDirNoFollow(dir string) (int, error) {
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("tool-result spill: open root: %w", err)
	}
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		if part == "" || part == "." {
			continue
		}
		next, err := syscall.Openat(fd, part,
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		_ = syscall.Close(fd)
		if err != nil {
			return -1, fmt.Errorf("tool-result spill: open directory component %q: %w", part, err)
		}
		fd = next
	}
	return fd, nil
}
