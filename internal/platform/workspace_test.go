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
	if a != b || WorkspaceSessionID(a) != WorkspaceSessionID(b) {
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
	if WorkspaceSessionID(otherCanonical) == WorkspaceSessionID(a) {
		t.Fatal("different workspaces shared an id")
	}
	if got := string(WorkspaceSessionID(a)); len(got) != len("workspace-v2-")+64 {
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

func TestWorkspaceSessionIDStableFixture(t *testing.T) {
	want := "workspace-v2-8e3755075f60db112335cfd5ca43a142d3514e96ca30d09a56ead6c6bb320331"
	if got := string(WorkspaceSessionID("/tmp/workspace")); got != want {
		t.Fatalf("fixture = %q", got)
	}
	const previous = "workspace-v1-32dabbe4792e0560188b21970685d0dc39524d366f759bd5abf87a77d2675e29"
	if want == previous {
		t.Fatal("v2 identity reused the v1 domain")
	}
}
