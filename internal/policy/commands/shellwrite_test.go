package commands_test

import (
	"testing"

	"github.com/smasonuk/falken-core/internal/policy/commands"
)

func TestHasShellWritePatterns(t *testing.T) {
	tests := []struct {
		command string
		want    bool
		wantErr bool
	}{
		{command: "echo hello"},
		{command: "echo hello > file", want: true},
		{command: "cat foo >> bar.log", want: true},
		{command: "tee out.txt", want: true},
		{command: "tee -a out.txt", want: true},
		{command: "/usr/bin/tee out.txt", want: true},
		{command: "command tee out.txt", want: true},
		{command: "env tee out.txt", want: true},
		{command: "env FOO=bar tee out.txt", want: true},
		{command: "tee >(grep err)"},
		{command: "tee >(cat > out)", want: true},
		{command: "cmd <(other)"},
		{command: "cmd <(other > inner)", want: true},
		{command: "cmd1 | cmd2"},
		{command: "(echo hi) > file", want: true},
		{command: `cmd "$(other > inner)"`, want: true},
		{command: "sh -c 'echo hi > file'", want: true},
		{command: "bash -c 'cat foo >> bar'", want: true},
		{command: "eval 'echo hi > file'", want: true},
		{command: "cmd <<EOF\nhi\nEOF"},
		{command: `"unclosed`, wantErr: true},
		{command: "ls; echo hi > file", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got, reasons, err := commands.HasShellWritePatterns(tt.command)
			if got != tt.want {
				t.Fatalf("HasShellWritePatterns() = %v, want %v; reasons=%+v err=%v", got, tt.want, reasons, err)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr=%v; reasons=%+v", err, tt.wantErr, reasons)
			}
		})
	}
}
