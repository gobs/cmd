package cmd

import (
	"github.com/gobs/readline"

	"fmt"
	"strings"
)

type Command struct {
	Name string
	Help string
	Call func(string) bool
}

type Completer struct {
	Words   []string
	Matches []string
}

func (c *Completer) Complete(prefix string, index int) string {
	if index == 0 {
		c.Matches = c.Matches[:0]
		no_prefix := len(prefix) == 0

		for _, w := range c.Words {
			if no_prefix || strings.HasPrefix(w, prefix) {
				c.Matches = append(c.Matches, w)
			}
		}
	}

	if index < len(c.Matches) {
		return c.Matches[index]
	} else {
		return ""
	}
}

func NewCompleter(words []string) (c *Completer) {
	c = new(Completer)
	c.Words = words
	c.Matches = make([]string, 0, len(c.Words))
	return
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

func (cmd *Cmd) addCommandCompleter() {
	names := make([]string, 0, len(cmd.Commands))

	for n, _ := range cmd.Commands {
		names = append(names, n)
	}

	completer := NewCompleter(names)
	readline.SetCompletionEntryFunction(completer.Complete)
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

	cmd.addCommandCompleter()

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
