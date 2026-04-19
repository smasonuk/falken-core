package host

import (
	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

func PrepareRuntimeState(paths runtimeapi.Paths) error {
	return paths.EnsureStateDirs()
}
