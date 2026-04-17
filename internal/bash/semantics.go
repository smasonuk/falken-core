package bash

import (
	"strings"
)

func InterpretExitCode(cmd string, exitCode int) (isError bool, message string) {
	if exitCode == 0 {
		return false, ""
	}

	cmdBase := strings.Split(cmd, " ")[0]
	switch cmdBase {
	case "grep", "rg":
		if exitCode == 1 {
			return false, "No matches found"
		}
	case "diff":
		if exitCode == 1 {
			return false, "Files differ"
		}
	case "test", "[":
		if exitCode == 1 {
			return false, "Condition is false"
		}
	case "find":
		if exitCode == 1 {
			return false, "Some directories were inaccessible"
		}
	}

	return true, ""
}
