package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsp1290/eino-agent/session"
)

const workspaceDomain = "eino-tui/workspace-session/v1\x00"

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

// WorkspaceSessionID avoids putting a raw local path in durable identifiers.
func WorkspaceSessionID(canonical string) session.ID {
	digest := sha256.Sum256([]byte(workspaceDomain + canonical))
	return session.ID("workspace-v1-" + hex.EncodeToString(digest[:]))
}
