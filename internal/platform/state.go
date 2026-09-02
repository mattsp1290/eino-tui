package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const StateOverride = "EINO_TUI_STATE_DIR"

type Paths struct {
	Directory string
	Database  string
}

// ResolveStateDir applies the supported platform precedence without reading globals.
func ResolveStateDir(env map[string]string, userHome, userConfigDir, goos string) (string, error) {
	if override := env[StateOverride]; override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("state directory override must be absolute")
		}
		return filepath.Clean(override), nil
	}
	switch goos {
	case "linux":
		if xdg := env["XDG_STATE_HOME"]; xdg != "" {
			if !filepath.IsAbs(xdg) {
				return "", fmt.Errorf("XDG_STATE_HOME must be absolute")
			}
			return filepath.Join(xdg, "eino-tui"), nil
		}
		if userHome == "" || !filepath.IsAbs(userHome) {
			return "", fmt.Errorf("absolute home directory required")
		}
		return filepath.Join(userHome, ".local", "state", "eino-tui"), nil
	case "darwin":
		if userConfigDir == "" || !filepath.IsAbs(userConfigDir) {
			return "", fmt.Errorf("absolute user config directory required")
		}
		return filepath.Join(userConfigDir, "eino-tui"), nil
	default:
		return "", fmt.Errorf("unsupported platform")
	}
}

func ProductionStateDir() (string, error) {
	home, _ := os.UserHomeDir()
	config, _ := os.UserConfigDir()
	env := map[string]string{
		StateOverride:    os.Getenv(StateOverride),
		"XDG_STATE_HOME": os.Getenv("XDG_STATE_HOME"),
	}
	return ResolveStateDir(env, home, config, runtime.GOOS)
}

// PrepareState creates and verifies the private state directory and database file.
func PrepareState(ctx context.Context, directory string) (Paths, error) {
	if err := ctx.Err(); err != nil {
		return Paths{}, err
	}
	if !filepath.IsAbs(directory) {
		return Paths{}, fmt.Errorf("state directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Paths{}, fmt.Errorf("create state directory: %w", err)
	}
	if err := secureDirectory(directory); err != nil {
		return Paths{}, err
	}
	database := filepath.Join(directory, "sessions.db")
	if err := secureDatabase(database); err != nil {
		return Paths{}, err
	}
	return Paths{Directory: directory, Database: database}, nil
}
