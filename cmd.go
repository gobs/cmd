/*
 This package is used to implement a "line oriented command interpreter", inspired by the python package with
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
	"github.com/gobs/args"
	"github.com/gobs/pretty"
	"github.com/gobs/readline"

	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
// The context for command completion
//
type Completer struct {
	// the list of words to match on
	Words []string
	// the list of current matches
	Matches []string
}

//
// Return a word matching the prefix
// If there are multiple matches, index selects which one to pick
//
func (c *Completer) Complete(prefix string, index int) string {
	if index == 0 {
		c.Matches = c.Matches[:0]

		for _, w := range c.Words {
			if strings.HasPrefix(w, prefix) {
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

//
// Create a Completer and initialize with list of words
//
func NewCompleter(words []string) (c *Completer) {
	c = new(Completer)
	c.Words = words
	c.Matches = make([]string, 0, len(c.Words))
	return
}

//
// This the the "context" for the command interpreter
//
type Cmd struct {
	// the prompt string
	Prompt string

	// the history file
	HistoryFile string

	// this function is called before starting the command loop
	PreLoop func()

	// this function is called before terminating the command loop
	PostLoop func()

	// this function is called before executing the selected command
	PreCmd func(string)

	// this function is called after a command has been executed
	// return true to terminate the interpreter, false to continue
	PostCmd func(string, bool) bool

	// this function is called if the last typed command was an empty line
	EmptyLine func()

	// this function is called if the command line doesn't match any existing command
	// by default it displays an error message
	Default func(string)

	// this function is called to implement command completion.
	// it should return a list of words that match the input text
	Complete func(string, string, int, int) []string

	// if true, enable shell commands
	EnableShell bool

	// this is the list of available commands indexed by command name
	Commands map[string]Command

	///////// private stuff /////////////
	completer    *Completer
	commandNames []string

	waitGroup          *sync.WaitGroup
	waitMax, waitCount int
}

func (cmd *Cmd) readHistoryFile() {
	if len(cmd.HistoryFile) == 0 {
		// no history file
		return
	}

	filepath := cmd.HistoryFile // start with current directory
	if _, err := os.Stat(filepath); err == nil {
		if err := readline.ReadHistoryFile(filepath); err != nil {
			fmt.Println(err)
		}

		return
	}

	filepath = path.Join(os.Getenv("HOME"), filepath) // then check home directory
	if _, err := os.Stat(filepath); err == nil {
		if err := readline.ReadHistoryFile(filepath); err != nil {
			fmt.Println(err)
		}
	}

	// update HistoryFile with home path
	cmd.HistoryFile = filepath
}

func (cmd *Cmd) writeHistoryFile() {
	if len(cmd.HistoryFile) == 0 {
		// no history file
		return
	}

	if err := readline.WriteHistoryFile(cmd.HistoryFile); err != nil {
		fmt.Println(err)
	}
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
	cmd.Add(Command{"echo", `echo input line`, cmd.Echo})
	cmd.Add(Command{"go", `go cmd: asynchronous execution of cmd, or 'go [--start|--wait]'`, cmd.Go})
	cmd.Add(Command{"repeat", `repeat [--count=n] [--wait=ms] [--echo] command args`, cmd.Repeat})
}

//
// Add a completer that matches on command names
//
func (cmd *Cmd) addCommandCompleter() {
	cmd.commandNames = make([]string, 0, len(cmd.Commands))

	for n, _ := range cmd.Commands {
		cmd.commandNames = append(cmd.commandNames, n)
	}

	// sorting for Help()
	sort.Strings(cmd.commandNames)

	cmd.completer = NewCompleter(cmd.commandNames)
	//readline.SetCompletionEntryFunction(completer.Complete)

	readline.SetAttemptedCompletionFunction(cmd.attemptedCompletion)
}

func (cmd *Cmd) attemptedCompletion(text string, start, end int) []string {
	if start == 0 { // this is the command to match
		return readline.CompletionMatches(text, cmd.completer.Complete)
	} else if cmd.Complete != nil {
		return cmd.Complete(text, readline.GetLineBuffer(), start, end)
	} else {
		return nil
	}
}

//
// execute shell command
//
func shellExec(command string) {
	args := args.GetArgs(command)
	if len(args) < 1 {
		fmt.Println("No command to exec")
	} else {
		cmd := exec.Command(args[0])
		cmd.Args = args
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Println(err)
		}
	}
}

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
	fmt.Println("")

	if len(line) == 0 {
		fmt.Println("Available commands (use 'help <topic>'):")
		fmt.Println("================================================================")

		tp := pretty.NewTabPrinter(8)

		for _, c := range cmd.commandNames {
			tp.Print(c)
		}

		tp.Println()
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

	fmt.Println("")
	return
}

func (cmd *Cmd) Echo(line string) (stop bool) {
	fmt.Println(line)
	return
}

func (cmd *Cmd) Go(line string) (stop bool) {
	if strings.HasPrefix(line, "-") {
		// should be --start or --wait

		args := args.ParseArgs(line)

		if _, ok := args.Options["start"]; ok {
			cmd.waitGroup = new(sync.WaitGroup)
			cmd.waitCount = 0
			cmd.waitMax = 0

			if len(args.Arguments) > 0 {
				cmd.waitMax, _ = strconv.Atoi(args.Arguments[0])
			}

			return
		}

		if _, ok := args.Options["wait"]; ok {
			if cmd.waitGroup == nil {
				fmt.Println("nothing to wait on")
			} else {
				cmd.waitGroup.Wait()
				cmd.waitGroup = nil
			}

			return
		}
	}

	if strings.HasPrefix(line, "go ") {
		fmt.Println("Don't go go me!")
	} else {
		if cmd.waitGroup == nil {
			go cmd.OneCmd(line)
		} else {
			if cmd.waitMax > 0 {
				if cmd.waitCount >= cmd.waitMax {
					cmd.waitGroup.Wait()
					cmd.waitCount = 0
				}
			}

			cmd.waitCount++
			cmd.waitGroup.Add(1)
			go func() {
				defer cmd.waitGroup.Done()
				cmd.OneCmd(line)
			}()
		}
	}

	return
}

func (cmd *Cmd) Repeat(line string) (stop bool) {
	count := ^uint64(0) // almost forever
	wait := 0           // no wait
	echo := false
	arg := ""

	for {
		if strings.HasPrefix(line, "-") {
			// some options
			parts := strings.SplitN(line, " ", 2)
			if len(parts) < 2 {
				// no command
				fmt.Println("nothing to repeat")
				return
			}

			arg, line = parts[0], parts[1]
			if arg == "--" {
				break
			}

			if arg == "--echo" {
				echo = true
			} else if strings.HasPrefix(arg, "--count=") {
				count, _ = strconv.ParseUint(arg[8:], 10, 64)
				fmt.Println("count", count)
			} else if strings.HasPrefix(arg, "--wait=") {
				wait, _ = strconv.Atoi(arg[7:])
				fmt.Println("wait", wait)
			} else {
				// unknown option
				fmt.Println("invalid option", arg)
				return
			}
		} else {
			break
		}
	}

	formatted := strings.Contains(line, "%")

	for i := uint64(0); i < count; i++ {
		command := line
		if formatted {
			command = fmt.Sprintf(line, i)
		}

		if echo {
			fmt.Println(cmd.Prompt, command)
		}

		if cmd.OneCmd(command) {
			break
		}

		if wait > 0 && i < count-1 {
			time.Sleep(time.Duration(wait) * time.Millisecond)
		}
	}

	return
}

//
// This method executes one command
//
func (cmd *Cmd) OneCmd(line string) (stop bool) {

	if cmd.EnableShell && strings.HasPrefix(line, "!") {
		shellExec(line[1:])
		return
	}

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

	cmd.addCommandCompleter()

	cmd.PreLoop()
	cmd.readHistoryFile()

	defer func() {
		cmd.writeHistoryFile()
		cmd.PostLoop()
	}()

	// loop until ReadLine returns nil (signalling EOF)
	for {
		result := readline.ReadLine(&cmd.Prompt)
		if result == nil {
			break
		}

		line := strings.TrimSpace(*result)

		if strings.HasPrefix(line, "#") {
			line = ""
		}

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
}
