package cmd

import (
	"github.com/gobs/readline"

	"fmt"
	"strings"
)

type Command func(string) bool

type Cmd struct {
	Prompt string

	PreLoop   func()
	PostLoop  func()
	PreCmd    func(string)
	PostCmd   func(string, bool) bool
	EmptyLine func()
	Default   func(string)

	Commands map[string]Command
}

func (cmd *Cmd) Init() {
	cmd.Commands = make(map[string]Command)

	if cmd.PreLoop == nil {
		cmd.PreLoop = func() {}
	}
	if cmd.PostLoop == nil {
		cmd.PostLoop = func() {}
	}
	if cmd.PreCmd == nil {
		cmd.PreCmd = func(string) {}
	}
	if cmd.PostCmd == nil {
		cmd.PostCmd = func(line string, stop bool) bool { return stop }
	}
	if cmd.EmptyLine == nil {
		cmd.EmptyLine = func() {}
	}
	if cmd.Default == nil {
		cmd.Default = func(line string) { fmt.Printf("invalid command: %v\n", line) }
	}
}

func (cmd *Cmd) OneCmd(line string) (stop bool) {

	parts := strings.SplitN(line, " ", 2)
	cname := parts[0]

	fn, ok := cmd.Commands[cname]

	if ok {
		var params string

		if len(parts) > 1 {
			params = strings.TrimSpace(parts[1])
		}

		stop = fn(params)
	} else {
		cmd.Default(line)
	}

	return
}

func (cmd *Cmd) CmdLoop() {
	if len(cmd.Prompt) == 0 {
		cmd.Prompt = "> "
	}

	cmd.PreLoop()

	// loop until ReadLine returns nil (signalling EOF)
	for {
		result := readline.ReadLine(&cmd.Prompt)
		if result == nil {
			break
		}

		line := strings.TrimSpace(*result)
		if line == "" {
			cmd.EmptyLine()
			continue
		}

		readline.AddHistory(*result) // allow user to recall this line

		cmd.PreCmd(line)

		stop := cmd.OneCmd(line)
		stop = cmd.PostCmd(line, stop)

		if stop {
			break
		}
	}

	cmd.PostLoop()
}
