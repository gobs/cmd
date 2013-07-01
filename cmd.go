/*
 This package is used to implement "line oriented command interpreter", inspired by the python package with
 the same name http://docs.python.org/2/library/cmd.html
 
 Usage:
 
 commander := &Cmd{...}
 commander.Init()
 
 commander.Add(Command{...})
 commander.Add(Command{...})
 
 commander.CmdLoop()
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

//
// This the the "context" for the command interpreter
//
type Cmd struct {
	// the prompt string
	Prompt string

	// this function is called before starting the command loop
	PreLoop   func()
	
	// this function is called before terminating the command loop
	PostLoop  func()
	
	// this function is called before executing the selected command
	PreCmd    func(string)
	
	// this function is called after a command has been executed
	// return true to terminate the interpreter, false to continue
	PostCmd   func(string, bool) bool
	
	// this function is called if the last typed command was an empty line
	EmptyLine func()
	
	// this function is called if the command line doesn't match any existing command
	// by default it displays an error message
	Default   func(string)

	// this is the list of available commands indexed by command name
	Commands map[string]Command
}

//
// Initialize the command interpreter context
//
func (cmd *Cmd) Init() {
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

	cmd.Commands = make(map[string]Command)
	cmd.Add(Command{"help", `list available commands`, cmd.Help})
}

//
// Add a command to the command interpreter.
// Overrides a command with the same name, if there was one
//
func (cmd *Cmd) Add(command Command) {
	cmd.Commands[command.Name] = command
}

//
// Default help command.
// It lists all available commands or it displays the help for the specified command
//
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

//
// This method executes one command
//
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

//
// This is the command interpreter entry point.
// It displays a prompt, waits for a command and executes it until the selected command returns true
//
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
