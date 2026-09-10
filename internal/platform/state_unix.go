//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func secureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("state path is not a secure directory")
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("state directory is not owned by current user")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect state directory: %w", err)
	}
	check, err := os.Lstat(path)
	if err != nil || !check.IsDir() || check.Mode().Perm() != 0o700 || !os.SameFile(info, check) {
		return fmt.Errorf("state directory verification failed")
	}
	return nil
}

func secureDatabase(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if createErr != nil {
			if errors.Is(createErr, os.ErrExist) {
				return secureDatabase(path)
			}
			return fmt.Errorf("create state database: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close state database: %w", closeErr)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect state database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("state database is not a regular file")
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("state database is not owned by current user")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect state database: %w", err)
	}
	check, err := os.Lstat(path)
	if err != nil || !check.Mode().IsRegular() || check.Mode().Perm() != 0o600 || !os.SameFile(info, check) {
		return fmt.Errorf("state database verification failed")
	}
	return nil
}

// ownedByCurrentUser is the shared ownership check for every private state
// file: directories, database files, and preference records.
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
