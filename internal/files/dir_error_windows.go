//go:build windows

package files

import "os"

func rollbackParentNotEmpty(err error) bool {
	return os.IsExist(err)
}
