package extensions

import "embed"

// EmbeddedExtensionsFS contains generated copies of the canonical extension sources under /extensions.
// Do not hand-edit files in internal/extensions/embedded_assets; regenerate them from the source tree.
//
//go:embed embedded_assets/tools/*/tool.yaml embedded_assets/tools/*/main.wasm embedded_assets/plugins/*/plugin.yaml embedded_assets/plugins/*/main.wasm
var EmbeddedExtensionsFS embed.FS
