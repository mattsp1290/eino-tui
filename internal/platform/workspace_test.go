package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalWorkspaceAndSessionIdentity(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "unicode-λ")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	a, err := CanonicalWorkspace(nested)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalWorkspace(link)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || WorkspaceID(a) != WorkspaceID(b) {
		t.Fatalf("identity mismatch: %q %q", a, b)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, nested)
	if err != nil {
		t.Fatal(err)
	}
	c, err := CanonicalWorkspace(relative)
	if err != nil || c != a {
		t.Fatalf("relative identity = %q, %v", c, err)
	}
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	otherCanonical, err := CanonicalWorkspace(other)
	if err != nil {
		t.Fatal(err)
	}
	if WorkspaceID(otherCanonical) == WorkspaceID(a) {
		t.Fatal("different workspaces shared an id")
	}
	if got := WorkspaceID(a); len(got) != len("workspace-")+64 {
		t.Fatalf("unexpected id %q", got)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalWorkspace(file); err == nil {
		t.Fatal("file accepted")
	}
	if _, err := CanonicalWorkspace(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing path accepted")
	}
}

func TestWorkspaceIDStableFixture(t *testing.T) {
	want := "workspace-9e4461b3f02ed91978850984d5f9d92ee0168b66eca5f063c8f0388aaae32be6"
	if got := WorkspaceID("/tmp/workspace"); got != want {
		t.Fatalf("fixture = %q", got)
	}
	if !ValidWorkspaceID(want) {
		t.Fatal("stable fixture does not match ValidWorkspaceID shape")
	}
}
