//go:build js && wasm

package main

import (
	"syscall/js"
)

func main() {
	js.Global().Set("tdDriveVersion", js.FuncOf(func(this js.Value, args []js.Value) any {
		return "td-wasm-dev"
	}))
	select {}
}
