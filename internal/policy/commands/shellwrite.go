package commands

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const maxShellWritePatternDepth = 4

// HasShellWritePatterns reports whether command contains direct shell syntax
// that writes files outside the managed file service.
func HasShellWritePatterns(command string) (bool, []Reason, error) {
	return hasShellWritePatterns(command, 0)
}

func hasShellWritePatterns(command string, depth int) (bool, []Reason, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return false, []Reason{{
			Code:   ReasonParseError,
			Detail: err.Error(),
		}}, err
	}

	var (
		found    bool
		reasons  []Reason
		firstErr error
	)
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Stmt:
			if hasShellWriteRedirect(node.Redirs) {
				found = true
				reasons = append(reasons, Reason{
					Code:   ReasonShellWriteRedirection,
					Detail: "shell output redirection writes directly to a file",
				})
			}
		case *syntax.CallExpr:
			args := callArgs(node)
			if isTeeWrite(args) {
				found = true
				reasons = append(reasons, Reason{
					Code:   ReasonShellWriteTee,
					Detail: fmt.Sprintf("command %q writes directly via tee", args[0]),
				})
			}
			if depth >= maxShellWritePatternDepth {
				return true
			}
			for _, nested := range nestedStaticShellCommands(node) {
				nestedFound, nestedReasons, err := hasShellWritePatterns(nested, depth+1)
				if err != nil && firstErr == nil {
					firstErr = err
				}
				if nestedFound {
					found = true
				}
				for _, reason := range nestedReasons {
					reason.Detail = "nested shell command: " + reason.Detail
					reasons = append(reasons, reason)
				}
			}
		}
		return true
	})

	return found, reasons, firstErr
}

func nestedStaticShellCommands(call *syntax.CallExpr) []string {
	args, ok := staticCallArgs(call)
	if !ok || len(args) == 0 {
		return nil
	}

	name := commandBase(args[0])
	switch name {
	case "sh", "bash":
		for i := 1; i < len(args); i++ {
			if !shellArgHasCFlag(args[i]) {
				continue
			}
			if i+1 < len(args) {
				return []string{args[i+1]}
			}
			return nil
		}
	case "eval":
		if len(args) > 1 {
			return []string{strings.Join(args[1:], " ")}
		}
	}
	return nil
}

func shellArgHasCFlag(arg string) bool {
	if arg == "-c" {
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	return strings.Contains(strings.TrimPrefix(arg, "-"), "c")
}

func staticCallArgs(call *syntax.CallExpr) ([]string, bool) {
	args := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		arg, ok := staticWordString(word)
		if !ok {
			return nil, false
		}
		args = append(args, arg)
	}
	return args, true
}

func staticWordString(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", true
	}
	return staticWordParts(word.Parts)
}

func staticWordParts(parts []syntax.WordPart) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			b.WriteString(part.Value)
		case *syntax.SglQuoted:
			b.WriteString(part.Value)
		case *syntax.DblQuoted:
			value, ok := staticWordParts(part.Parts)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		default:
			return "", false
		}
	}
	return b.String(), true
}

func commandBase(command string) string {
	command = strings.TrimSpace(command)
	if idx := strings.LastIndex(command, "/"); idx >= 0 {
		return command[idx+1:]
	}
	return command
}
