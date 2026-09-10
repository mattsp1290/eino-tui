package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveStateDir(t *testing.T) {
	if got, err := ResolveStateDir(map[string]string{StateOverride: "/tmp/custom"}, "/home/me", "/config", "linux"); err != nil || got != "/tmp/custom" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ResolveStateDir(map[string]string{StateOverride: "relative"}, "/home/me", "/config", "linux"); err == nil {
		t.Fatal("relative override accepted")
	}
	if got, err := ResolveStateDir(nil, "/home/me", "/config", "linux"); err != nil || got != "/home/me/.local/state/eino-tui" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := ResolveStateDir(nil, "/home/me", "/config", "darwin"); err != nil || got != "/config/eino-tui" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPrepareStatePermissionsAndSymlinkRejection(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix permissions")
	}
	dir := filepath.Join(t.TempDir(), "state")
	paths, err := PrepareState(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{paths.Directory: 0o700, paths.Database: 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(t.TempDir(), "bad")
	if err := os.Mkdir(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(badDir, DatabaseFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareState(context.Background(), badDir); err == nil {
		t.Fatal("database symlink accepted")
	}
	broadDir := filepath.Join(t.TempDir(), "broad")
	if err := os.Mkdir(broadDir, 0o777); err != nil {
		t.Fatal(err)
	}
	broadDB := filepath.Join(broadDir, DatabaseFile)
	if err := os.WriteFile(broadDB, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	paths, err = PrepareState(context.Background(), broadDir)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(paths.Directory); info.Mode().Perm() != 0o700 {
		t.Fatalf("directory not tightened: %o", info.Mode().Perm())
	}
	if info, _ := os.Stat(paths.Database); info.Mode().Perm() != 0o600 {
		t.Fatalf("database not tightened: %o", info.Mode().Perm())
	}
	directoryTarget := t.TempDir()
	directoryLink := filepath.Join(t.TempDir(), "linked-state")
	if err := os.Symlink(directoryTarget, directoryLink); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareState(context.Background(), directoryLink); err == nil {
		t.Fatal("state directory symlink accepted")
	}
	fileState := filepath.Join(t.TempDir(), "file-state")
	if err := os.WriteFile(fileState, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareState(context.Background(), fileState); err == nil {
		t.Fatal("regular file accepted as state directory")
	}
}
