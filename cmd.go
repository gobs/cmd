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

	"github.com/peterh/liner"

	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	reArg = regexp.MustCompile(`\$(\w+|\(\w+\)|[\*#]|\([\*#]\))`)
	sep   = string(0xFFFD) // unicode replacement char
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
	// the function to call to print the help string
	HelpFunc func()
}

func (c *Command) DefaultHelp() {
	if len(c.Help) > 0 {
		fmt.Println(c.Help)
	} else {
		fmt.Println("No help for ", c.Name)
	}
}

//
// The context for command completion
//
type Completer struct {
	// the list of words to match on
	Words []string
}

func (c *Completer) Complete(line string) (matches []string) {
	for _, w := range c.Words {
		if strings.HasPrefix(w, line) {
			matches = append(matches, w)
		}
	}

	return
}

//
// Create a Completer and initialize with list of words
//
func NewCompleter(words []string) (c *Completer) {
	return &Completer{Words: words}
}

//
// This the the "context" for the command interpreter
//
type Cmd struct {
	// the prompt string
	Prompt string

	// the continuation prompt string
	ContinuationPrompt string

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
	Complete func(string, string) []string

	// this function is called when the user tries to interrupt a running
	// command. If it returns true, the application will be terminated.
	Interrupt func(os.Signal) bool

	// if true, enable shell commands
	EnableShell bool

	// if true, print elapsed time
	Timing bool

	// if true, a Ctrl-C should return an error
	// CtrlCAborts bool

	// this is the list of available commands indexed by command name
	Commands map[string]Command

	// list of variables
	Vars map[string]string

	///////// private stuff /////////////
	line         *liner.State
	completer    *Completer
	commandNames []string
	functions    map[string][]string

	waitGroup          *sync.WaitGroup
	waitMax, waitCount int
}

func (cmd *Cmd) readHistoryFile() {
	if len(cmd.HistoryFile) == 0 {
		// no history file
		return
	}

	filepath := cmd.HistoryFile // start with current directory
	if f, err := os.Open(filepath); err == nil {
		cmd.line.ReadHistory(f)
		f.Close()

		cmd.HistoryFile = filepath
		return
	}

	filepath = path.Join(os.Getenv("HOME"), filepath) // then check home directory
	if f, err := os.Open(filepath); err == nil {
		cmd.line.ReadHistory(f)
		f.Close()

		cmd.HistoryFile = filepath
		return
	}
}

func (cmd *Cmd) writeHistoryFile() {
	if len(cmd.HistoryFile) == 0 {
		// no history file
		return
	}

	if f, err := os.Create(cmd.HistoryFile); err == nil {
		cmd.line.WriteHistory(f)
		f.Close()
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
	if cmd.Interrupt == nil {
		cmd.Interrupt = func(sig os.Signal) bool { return true }
	}

	cmd.Commands = make(map[string]Command)
	cmd.Add(Command{"help", `list available commands`, cmd.Help, nil})
	cmd.Add(Command{"echo", `echo input line`, cmd.Echo, nil})
	cmd.Add(Command{"go", `go cmd: asynchronous execution of cmd, or 'go [--start|--wait]'`, cmd.Go, nil})
	cmd.Add(Command{"repeat", `repeat [--count=n] [--wait=ms] [--echo] command args`, cmd.Repeat, nil})
	cmd.Add(Command{"function", `function name body`, cmd.Function, nil})
	cmd.Add(Command{"var", `var name value`, cmd.Variable, nil})

	cmd.functions = make(map[string][]string)
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
	cmd.line.SetWordCompleter(cmd.wordCompleter)
}

func (cmd *Cmd) wordCompleter(line string, pos int) (head string, completions []string, tail string) {
	start := strings.LastIndex(line[:pos], " ")
	if start < 0 { // this is the command to match
		return "", cmd.completer.Complete(line), line[pos:]
	} else if strings.HasPrefix(line, "help ") {
		return line[:start+1], cmd.completer.Complete(line[start+1:]), line[pos:]
	} else if cmd.Complete != nil {
		return line[:start+1], cmd.Complete(line[start+1:], line), line[pos:]
	} else {
		return
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
	if command.HelpFunc == nil {
		command.HelpFunc = command.DefaultHelp
	}

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
			c.HelpFunc()
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

		fmt.Println("invalid option")
		return
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

			arg, line = parts[0], strings.TrimSpace(parts[1])
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

func (cmd *Cmd) Function(line string) (stop bool) {
	// function
	if line == "" {
		if len(cmd.functions) == 0 {
			fmt.Println("no functions")
		} else {
			fmt.Println("functions:")
			for fn, _ := range cmd.functions {
				fmt.Println(" ", fn)
			}
		}
		return
	}

	parts := strings.SplitN(line, " ", 2)
	// function name
	if len(parts) == 1 {
		fn := parts[0]
		body, ok := cmd.functions[fn]
		if !ok {
			fmt.Println("no function", fn)
		} else {
			fmt.Println("function", fn, "{")
			for _, l := range body {
				fmt.Println(" ", l)
			}
			fmt.Println("}")
		}
		return
	}

	// function name body
	fname, body := parts[0], strings.TrimSpace(parts[1])
	if body == "--delete" {
		if _, ok := cmd.functions[fname]; ok {
			delete(cmd.functions, fname)
			fmt.Println("function", fname, "deleted")
		} else {
			fmt.Println("no function", fname)
		}

		return
	}

	if !strings.HasSuffix(body, "{") { // one line body
		body := strings.Replace(body, "\\$", "$", -1) // for one-liners variables should be escaped
		cmd.functions[fname] = []string{body}
		return
	}

	if body != "{" { // we can't do inline command + body
		fmt.Println("unexpected body and block")
		return
	}

	var lines []string

	for {

		l, err := cmd.line.Prompt(cmd.ContinuationPrompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}

		l = strings.TrimSpace(l)
		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		if l == "}" { // close block
			break
		}

		lines = append(lines, l)
	}

	cmd.functions[fname] = lines
	// fmt.Println("function", fname, lines)
	return
}

func (cmd *Cmd) Variable(line string) (stop bool) {
	// var
	if line == "" {
		if len(cmd.Vars) == 0 {
			fmt.Println("no varaibles")
		} else {
			fmt.Println("variables:")
			for k, v := range cmd.Vars {
				fmt.Println(" ", k, "=", v)
			}
		}

		return
	}

	parts := strings.SplitN(line, " ", 2)
	name := parts[0]

	// var name value
	if len(parts) == 2 {
		cmd.Vars[name] = strings.TrimSpace(parts[1])
	}

	value, ok := cmd.Vars[name]
	if !ok {
		fmt.Println("no variable", name)
	} else {
		fmt.Println(name, "=", value)
	}
	return
}

//
// This method executes one command
//
func (cmd *Cmd) OneCmd(line string) (stop bool) {
	if cmd.Timing {
		start := time.Now()
		defer func() {
			fmt.Println("Elapsed:", time.Since(start).Truncate(time.Millisecond))
		}()
	}

	if cmd.EnableShell && strings.HasPrefix(line, "!") {
		shellExec(line[1:])
		return
	}

	var cname, params string

	parts := strings.SplitN(line, " ", 2)

	cname = parts[0]
	if len(parts) > 1 {
		params = strings.TrimSpace(parts[1])

		if cname != "function" { // XXX: don't expand one-line body of "function"
			params = cmd.expandVariables(params, nil)
		}
	}

	if command, ok := cmd.Commands[cname]; ok {
		stop = command.Call(params)
	} else if function, ok := cmd.functions[cname]; ok {
		cmd.runFunction(cname, function, args.GetArgs(params))
	} else {
		cmd.Default(line)
	}

	return
}

func (cmd *Cmd) expandVariables(line string, args []string) string {
	line = reArg.ReplaceAllStringFunc(line, func(s string) string {
		// ReplaceAll doesn't return submatches so we need to cleanup
		arg := strings.TrimLeft(s, "$(")
		arg = strings.TrimRight(arg, ")")

		if v, ok := cmd.Vars[arg]; ok {
			return v
		}

		if len(args) > 0 {
			// argument substitutions (for functions)
			//fmt.Println("var", arg, args)

			if arg == "*" {
				return strings.Join(args[1:], " ") // all args
			}

			if arg == "#" {
				return strconv.Itoa(len(args) - 1) // args[0] is the name
			}

			if n, err := strconv.Atoi(arg); err == nil && n >= 0 && n < len(args) {
				return args[n]
			}
		}

		return ""
	})

	// fmt.Println("expandVariable", line)
	return line
}

func (cmd *Cmd) runFunction(name string, body []string, args []string) {
	args = append([]string{name}, args...)

	for _, line := range body {
		line = cmd.expandVariables(line, args)
		_ = cmd.OneCmd(line)
	}
}

//
// This is the command interpreter entry point.
// It displays a prompt, waits for a command and executes it until the selected command returns true
//
func (cmd *Cmd) CmdLoop() {
	if len(cmd.Prompt) == 0 {
		cmd.Prompt = "> "
	}
	if len(cmd.ContinuationPrompt) == 0 {
		cmd.ContinuationPrompt = ": "
	}

	cmd.line = liner.NewLiner()
	defer cmd.line.Close()

	// cmd.line.SetCtrlCAborts(cmd.CtrlCAborts)

	cmd.addCommandCompleter()
	cmd.PreLoop()
	cmd.readHistoryFile()

	defer func() {
		if cmd.line != nil {
			cmd.writeHistoryFile()
			cmd.line.Close()
		}

		cmd.PostLoop()
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigc

		// restore terminal
		if cmd.line != nil {
			cmd.line.Close()
		}

		signal.Stop(sigc)

		if cmd.Interrupt(sig) {
			// rethrow signal to kill app
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(sig)
		} else {
			signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		}
	}()

	// loop until ReadLine returns nil (signalling EOF)
main_loop:
	for {
		line, err := cmd.line.Prompt(cmd.Prompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			break
		}

		line = strings.TrimSpace(line)

		//
		// merge lines ending with '\' into one single line
		//
		for strings.HasSuffix(line, "\\") { // continuation
			line = strings.TrimRight(line, "\\")
			line = strings.TrimSpace(line)

			l, err := cmd.line.Prompt(cmd.ContinuationPrompt)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				break main_loop
			}

			line += " " + strings.TrimSpace(l)
		}

		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		if cmd.line != nil {
			cmd.line.AppendHistory(line) // allow user to recall this line
		}

		m, _ := liner.TerminalMode()

		cmd.PreCmd(line)
		stop := cmd.OneCmd(line)
		stop = cmd.PostCmd(line, stop)

		if m != nil {
			m.ApplyMode()
		}

		if stop {
			break
		}
	}
}
