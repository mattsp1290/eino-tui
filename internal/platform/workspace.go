package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const workspaceIdentityDomain = "eino-tui/workspace-identity/v1\x00"

var workspaceIDPattern = regexp.MustCompile(`^workspace-[0-9a-f]{64}$`)

// Workspace is the immutable launch identity shared by every conversation
// created from one canonical directory. ID never contains the raw path.
type Workspace struct {
	Root string
	ID   string
}

// CanonicalWorkspace resolves an existing directory to one stable launch path.
func CanonicalWorkspace(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory")
	}
	return resolved, nil
}

// IdentifyWorkspace canonicalizes a launch path and derives its stable identity.
// Absolute, relative, and symlink spellings of one directory share an identity.
func IdentifyWorkspace(path string) (Workspace, error) {
	canonical, err := CanonicalWorkspace(path)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{Root: canonical, ID: WorkspaceID(canonical)}, nil
}

// WorkspaceID hashes the canonical path so durable records and preference
// filenames never carry a raw local path. Conversation IDs are allocated
// separately and randomly; this value only scopes them.
func WorkspaceID(canonical string) string {
	digest := sha256.Sum256([]byte(workspaceIdentityDomain + canonical))
	return "workspace-" + hex.EncodeToString(digest[:])
}

// ValidWorkspaceID reports whether a value has the exact shape produced by
// WorkspaceID, which keeps preference filenames path-safe.
func ValidWorkspaceID(value string) bool { return workspaceIDPattern.MatchString(value) }
