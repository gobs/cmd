/*
 This package is used to implement "line oriented command interpreter", inspired by the python package with
 the same name http://docs.python.org/2/library/cmd.html
 */
package cmd

import (
	"github.com/gobs/readline"

	"fmt"
	"strings"
)

//
// This is used to describe a new command
//
type Command struct {
	// command name
	Name string
	// command description
	Help string
	// the function to call to execute the command
	Call func(string) bool
}

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

	cmd.Add(Command{"help", `list available commands`, cmd.Help})
}

func (cmd *Cmd) Add(command Command) {
	cmd.Commands[command.Name] = command
}

func (cmd *Cmd) Help(line string) (stop bool) {
	if len(line) == 0 {
		fmt.Println("Available commands:")

		for k, _ := range cmd.Commands {
			fmt.Println("    ", k)
		}
	} else {
		c, ok := cmd.Commands[line]
		if ok {
			if len(c.Help) > 0 {
				fmt.Println(c.Help)
			} else {
				fmt.Println("No help for ", line)
			}
		} else {
			fmt.Println("unknown command")
		}
	}
	return
}

func (cmd *Cmd) OneCmd(line string) (stop bool) {

	parts := strings.SplitN(line, " ", 2)
	cname := parts[0]

	command, ok := cmd.Commands[cname]

	if ok {
		var params string

		if len(parts) > 1 {
			params = strings.TrimSpace(parts[1])
		}

		stop = command.Call(params)
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
