package commands

import (
	"bytes"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ReasonCode identifies why shell-write detection matched or failed to parse.
type ReasonCode string

const (
	ReasonParseError            ReasonCode = "parse_error"
	ReasonShellWriteRedirection ReasonCode = "shell_write_redirection"
	ReasonShellWriteTee         ReasonCode = "shell_write_tee"
)

// Reason provides structured explanation for a shell-write detection result.
type Reason struct {
	Code   ReasonCode
	Detail string
}

func hasShellWriteRedirect(redirs []*syntax.Redirect) bool {
	for _, redir := range redirs {
		if redir == nil {
			continue
		}
		switch redir.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob, syntax.AppClob, syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
			return true
		}
	}
	return false
}

func isTeeWrite(args []string) bool {
	args, ok := teeCommandArgs(args)
	if !ok {
		return false
	}

	nonFlagArgs := 0
	stopFlags := false
	for _, arg := range args[1:] {
		if stopFlags {
			if !isProcessSubstitution(arg) {
				nonFlagArgs++
			}
			continue
		}
		if arg == "--" {
			stopFlags = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !isProcessSubstitution(arg) {
			nonFlagArgs++
		}
	}

	return nonFlagArgs > 0
}

func teeCommandArgs(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}

	switch commandBase(args[0]) {
	case "tee":
		return args, true
	case "command", "builtin":
		for i := 1; i < len(args); i++ {
			if args[i] == "--" {
				i++
				if i < len(args) && commandBase(args[i]) == "tee" {
					return args[i:], true
				}
				return nil, false
			}
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			if commandBase(args[i]) == "tee" {
				return args[i:], true
			}
			return nil, false
		}
	case "env":
		for i := 1; i < len(args); i++ {
			if args[i] == "--" {
				i++
				if i < len(args) && commandBase(args[i]) == "tee" {
					return args[i:], true
				}
				return nil, false
			}
			if strings.HasPrefix(args[i], "-") || strings.Contains(args[i], "=") {
				continue
			}
			if commandBase(args[i]) == "tee" {
				return args[i:], true
			}
			return nil, false
		}
	}

	return nil, false
}

func isProcessSubstitution(arg string) bool {
	return strings.HasPrefix(arg, ">(") || strings.HasPrefix(arg, "<(")
}

func callArgs(call *syntax.CallExpr) []string {
	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		args = append(args, wordString(arg))
	}
	return args
}

func wordString(word *syntax.Word) string {
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	_ = printer.Print(&buf, word)
	return buf.String()
}
