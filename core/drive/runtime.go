package drive

import (
	"github.com/thedavidweng/tg-drive-cli/core/ports"
)

// Runtime wires shared core ports for native CLI, desktop, and future WASM hosts.
type Runtime struct {
	Store    ports.Store
	Files    ports.FileSystem
	Telegram ports.Telegram
}

// NewRuntime assembles a drive runtime from injected adapters.
func NewRuntime(store ports.Store, files ports.FileSystem, tg ports.Telegram) *Runtime {
	return &Runtime{Store: store, Files: files, Telegram: tg}
}
