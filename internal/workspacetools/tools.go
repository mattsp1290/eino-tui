// Package workspacetools owns the exact read-only tool capability exposed by
// eino-tui. Catalog mounting and run selection deliberately live together so
// callers cannot accidentally widen the provider-visible tool set.
package workspacetools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/extension"
	"github.com/mattsp1290/eino-agent/tools/einotools"
	"github.com/mattsp1290/eino-tools/catalog"
	"github.com/mattsp1290/eino-tools/fileops"
	"github.com/mattsp1290/eino-tools/glob"
	"github.com/mattsp1290/eino-tools/search"
	"github.com/mattsp1290/eino-tools/shell"
)

const (
	componentInstance = "eino-tui-read-only-tools"
	catalogVersion    = "v0.1.1-0.20260907205433-99b7b6adda67"
	// These identities change only when the host adapter contract or ordered
	// allowlist changes. They are SHA-256 digests of
	// "eino-tui-eino-tools-adapter-contract-v1" and
	// "read-only:file_read,file_list,glob,search:v1", respectively. Executable
	// identities are captured separately by eino-tools.
	adapterHash = "bb52bf4132f226f1e3c3e02e68cf9f63b4ad71adfefdf0caff78e1b070db25fe"
	configHash  = "5d05927657c89952971c37a3c6fb4374b0303075ab44f3afec6016feabef11a9"
)

var enabledNames = []string{fileops.NameRead, fileops.NameList, glob.Name, search.Name}

// The pinned catalog clones an empty non-nil slice with append(nil, empty...),
// which collapses it to nil and would inherit the process environment. A
// single empty PATH entry preserves the intended minimal, deterministic
// environment while absolute executable paths remain authoritative.
func minimalEnvironment() []string { return []string{"PATH="} }

// EnabledNames returns the deterministic provider-facing allowlist.
func EnabledNames() []string { return append([]string(nil), enabledNames...) }

// Config returns an isolated, explicitly non-nil tool selection.
func Config() config.ToolConfig { return config.ToolConfig{Enabled: EnabledNames()} }

// Mount publishes the standard catalog. The run configuration still selects
// only EnabledNames; unselected catalog entries can never reach a run plan.
func Mount(ctx context.Context, registry *composition.Registry) (*composition.Mount, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("resolve ripgrep: %w", err)
	}
	rg, err = filepath.Abs(rg)
	if err != nil {
		return nil, fmt.Errorf("resolve ripgrep path: %w", err)
	}
	return mountWithExecutables(ctx, registry, rg, "/bin/sh")
}

func mountWithExecutables(ctx context.Context, registry *composition.Registry, rg, shellBinary string) (*composition.Mount, error) {
	component := extension.Component{
		InstanceID: componentInstance,
		Artifact: extension.Artifact{
			Name: "eino-tools-standard", Version: catalogVersion,
			Hash: adapterHash, ConfigHash: configHash, SourceKind: extension.SourceNative,
		},
	}
	return einotools.MountStandard(ctx, registry, component, einotools.Options{
		Scope: extension.GlobalScope(),
		Catalog: catalog.Options{
			SearchOptions: &search.Options{RGBinary: rg, Env: minimalEnvironment(), DisableConfig: true},
			ShellOptions:  &shell.Options{ShellBinary: shellBinary, Env: minimalEnvironment()},
		},
	})
}
