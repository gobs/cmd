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
	"github.com/gobs/cmd/internal"
	"github.com/gobs/pretty"

	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	reArg       = regexp.MustCompile(`\$(\w+|\(\w+\)|\(env.\w+\)|[\*#]|\([\*#]\))`) // $var or $(var)
	reVarAssign = regexp.MustCompile(`([\d\w]+)(=(.*))?`)                           // name=value
	sep         = string(0xFFFD)                                                    // unicode replacement char

	// NoVar is passed to Command.OnChange to indicate that the variable is not set or needs to be deleted
	NoVar = &struct{}{}
)

type arguments = map[string]string

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

type Completer interface {
	Complete(string, string) []string // Complete(start, full-line) returns matches
}

type linkedCompleter struct {
	name      string
	completer Completer
	next      *linkedCompleter
}

type CompleterWords func() []string
type CompleterCond func(start, line string) bool

//
// The context for command completion
//
type WordCompleter struct {
	// a function that returns the list of words to match on
	Words CompleterWords
	// a function that returns true if this completer should be executed
	Cond CompleterCond
}

func (c *WordCompleter) Complete(start, line string) (matches []string) {
	if c.Cond != nil && c.Cond(start, line) == false {
		return
	}

	for _, w := range c.Words() {
		if strings.HasPrefix(w, start) {
			matches = append(matches, w)
		}
	}

	return
}

//
// Create a WordCompleter and initialize with list of words
//
func NewWordCompleter(words CompleterWords, cond CompleterCond) *WordCompleter {
	return &WordCompleter{Words: words, Cond: cond}
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

	// this function is called to execute one command
	OneCmd func(string) bool

	// this function is called if the last typed command was an empty line
	EmptyLine func()

	// this function is called if the command line doesn't match any existing command
	// by default it displays an error message
	Default func(string)

	// this function is called when the user types the "help" command.
	// It is implemented so that it can be overwritten, mainly to support plugins.
	Help func(string) bool

	// this function is called to implement command completion.
	// it should return a list of words that match the input text
	Complete func(string, string) []string

	// this function is called when a variable change (via set/var command).
	// it should return the new value to set the variable to (to force type casting)
	//
	// oldv will be nil if a new varabile is being created
	//
	// newv will be nil if the variable is being deleted
	OnChange func(name string, oldv, newv interface{}) interface{}

	// this function is called when the user tries to interrupt a running
	// command. If it returns true, the application will be terminated.
	Interrupt func(os.Signal) bool

	// if true, enable shell commands
	EnableShell bool

	// if true, print elapsed time
	Timing bool

	// if true, print command before executing
	Echo bool

	// if true, don't print result of some operations (stored in result variables)
	Silent bool

	// if true, a Ctrl-C should return an error
	// CtrlCAborts bool

	// this is the list of available commands indexed by command name
	Commands map[string]Command

	///////// private stuff /////////////
	completers *linkedCompleter

	commandNames      []string
	commandCompleter  *WordCompleter
	functionCompleter *WordCompleter

	waitGroup          *sync.WaitGroup
	waitMax, waitCount int

	interrupted bool
	context     *internal.Context
	stdout      *os.File // original stdout
	sync.RWMutex
}

//
// Initialize the command interpreter context
//
func (cmd *Cmd) Init(plugins ...Plugin) {
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
	if cmd.OneCmd == nil {
		cmd.OneCmd = cmd.oneCmd
	}
	if cmd.EmptyLine == nil {
		cmd.EmptyLine = func() {}
	}
	if cmd.Default == nil {
		cmd.Default = func(line string) { fmt.Printf("invalid command: %v\n", line) }
	}
	if cmd.OnChange == nil {
		cmd.OnChange = func(name string, oldv, newv interface{}) interface{} { return newv }
	}
	if cmd.Interrupt == nil {
		cmd.Interrupt = func(sig os.Signal) bool { return true }
	}
	if cmd.Help == nil {
		cmd.Help = cmd.help
	}
	cmd.context = internal.NewContext()
	cmd.context.PushScope(nil, nil)

	cmd.stdout = os.Stdout

	cmd.Commands = make(map[string]Command)
	cmd.Add(Command{"help", `list available commands`, func(line string) bool {
		return cmd.Help(line)
	}, nil})
	cmd.Add(Command{"echo", `echo input line`, cmd.command_echo, nil})
	cmd.Add(Command{"go", `go cmd: asynchronous execution of cmd, or 'go [--start|--wait]'`, cmd.command_go, nil})
	cmd.Add(Command{"time", `time [starttime]`, cmd.command_time, nil})
	cmd.Add(Command{"output", `output [filename|--]`, cmd.command_output, nil})
	cmd.Add(Command{"exit", `exit program`, cmd.command_exit, nil})

	for _, p := range plugins {
		if err := p.PluginInit(cmd, cmd.context); err != nil {
			panic("plugin initialization failed: " + err.Error())
		}
	}

	cmd.SetVar("echo", cmd.Echo)
	cmd.SetVar("print", !cmd.Silent)
	cmd.SetVar("timing", cmd.Timing)
}

func (cmd *Cmd) setInterrupted(interrupted bool) {
	cmd.Lock()
	cmd.interrupted = interrupted
	cmd.Unlock()
}

func (cmd *Cmd) Interrupted() (interrupted bool) {
	cmd.RLock()
	interrupted = cmd.interrupted
	cmd.RUnlock()
	return
}

//
// Plugin is the interface implemented by plugins
//
type Plugin interface {
	PluginInit(cmd *Cmd, ctx *internal.Context) error
}

func (cmd *Cmd) SetPrompt(prompt string, max int) {
	l := len(prompt)

	if max > 3 && l > max {
		max -= 3 // for "..."
		prompt = "..." + prompt[l-max:]
	}

	cmd.Prompt = prompt
}

//
// Update function completer (when function list changes)
//
func (cmd *Cmd) updateCompleters() {
	if c := cmd.GetCompleter(""); c == nil { // default completer
		cmd.commandNames = make([]string, 0, len(cmd.Commands))
		for name := range cmd.Commands {
			cmd.commandNames = append(cmd.commandNames, name)
		}
		sort.Strings(cmd.commandNames) // for help listing

		cmd.AddCompleter("", NewWordCompleter(func() []string {
			return cmd.commandNames
		}, func(s, l string) bool {
			return s == l // check if we are at the beginning of the line
		}))

		cmd.AddCompleter("help", NewWordCompleter(func() []string {
			return cmd.commandNames
		}, func(s, l string) bool {
			return strings.HasPrefix(l, "help ")
		}))
	}
}

func (cmd *Cmd) wordCompleter(line string, pos int) (head string, completions []string, tail string) {
	start := strings.LastIndex(line[:pos], " ")

	for c := cmd.completers; c != nil; c = c.next {
		if completions = c.completer.Complete(line[start+1:], line); completions != nil {
			return line[:start+1], completions, line[pos:]
		}
	}

	if cmd.Complete != nil {
		return line[:start+1], cmd.Complete(line[start+1:], line), line[pos:]
	}

	return
}

func (cmd *Cmd) AddCompleter(name string, c Completer) {
	lc := &linkedCompleter{name: name, completer: c, next: cmd.completers}
	cmd.completers = lc
}

func (cmd *Cmd) GetCompleter(name string) Completer {
	for c := cmd.completers; c != nil; c = c.next {
		if c.name == name {
			return c.completer
		}
	}

	return nil
}

//
// execute shell command
//
func shellExec(command string) {
	args := args.GetArgs(command)
	if len(args) < 1 {
		fmt.Println("No command to exec")
	} else {
		if strings.ContainsAny(command, "$*~") {
			if _, err := exec.LookPath("sh"); err == nil {
				args = []string{"sh", "-c", command}
			}
		}
		cmd := exec.Command(args[0])
		cmd.Args = args
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Println(err)
		}
	}
}

//
// execute shell command and pipe input and/or output
//
func pipeExec(command string) *os.File {
	args := args.GetArgs(command)
	if len(args) < 1 {
		fmt.Println("No command to exec")
	} else {
		if strings.ContainsAny(command, "$*~") {
			if _, err := exec.LookPath("sh"); err == nil {
				args = []string{"sh", "-c", command}
			}
		}
		cmd := exec.Command(args[0])
		cmd.Args = args
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		pr, pw, err := os.Pipe()
		if err != nil {
			fmt.Println("cannot create pipe:", err)
			return nil
		}

		cmd.Stdin = pr

		go func() {
			if err := cmd.Run(); err != nil {
				fmt.Println(err)
			}
		}()

		return pw
	}

	return nil
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
func (cmd *Cmd) help(line string) (stop bool) {
	fmt.Println("")

	if line == "--all" {
		fmt.Println("Available commands (use 'help <topic>'):")
		fmt.Println("================================================================")
		for _, c := range cmd.commandNames {
			fmt.Printf("%v: ", c)
			cmd.Commands[c].HelpFunc()
		}
	} else if len(line) == 0 {
		fmt.Println("Available commands (use 'help <topic>'):")
		fmt.Println("================================================================")

		max := 0

		for _, c := range cmd.commandNames {
			if len(c) > max {
				max = len(c)
			}
		}

		tp := pretty.NewTabPrinter(80 / (max + 1))
		tp.TabWidth(max + 1)

		for _, c := range cmd.commandNames {
			tp.Print(c)
		}
		tp.Println()
	} else {
		if c, ok := cmd.Commands[line]; ok {
			c.HelpFunc()
		} else {
			fmt.Println("unknown command or function")
		}
	}

	fmt.Println("")
	return
}

func (cmd *Cmd) command_echo(line string) (stop bool) {
	if strings.HasPrefix(line, "-n ") {
		fmt.Print(strings.TrimSpace(line[3:]))
	} else {
		fmt.Println(line)
	}
	return
}

func (cmd *Cmd) command_go(line string) (stop bool) {
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

func (cmd *Cmd) command_time(line string) (stop bool) {
	if line == "-m" || line == "--milli" || line == "--millis" {
		t := time.Now().UnixNano() / int64(time.Millisecond)
		if !cmd.SilentResult() {
			fmt.Println(t)
		}

		cmd.SetVar("time", t)
	} else if line == "" {
		t := time.Now().Format(time.RFC3339)
		if !cmd.SilentResult() {
			fmt.Println(t)
		}

		cmd.SetVar("time", t)
	} else {
		t, err := time.Parse(time.RFC3339, line)
		if err != nil {
			fmt.Println("invalid start time")
		} else {
			d := time.Since(t).Round(time.Millisecond)
			if !cmd.SilentResult() {
				fmt.Println(d)
			}
			cmd.SetVar("elapsed", d.Seconds())
		}
	}

	return
}

func (cmd *Cmd) command_output(line string) (stop bool) {
	if line != "" {
		if line == "--" {
			if cmd.stdout != nil && os.Stdout != cmd.stdout { // default stdout
				os.Stdout.Close()
				os.Stdout = cmd.stdout
			}
		} else if strings.HasPrefix(line, "|") { // pipe
			line = strings.TrimSpace(line[1:])

			w := pipeExec(line)
			if w == nil {
				return
			}

			if cmd.stdout == nil {
				cmd.stdout = os.Stdout
			} else if cmd.stdout != os.Stdout {
				os.Stdout.Close()
			}

			os.Stdout = w
		} else {
			f, err := os.Create(line)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return
			}

			if cmd.stdout == nil {
				cmd.stdout = os.Stdout
			} else if cmd.stdout != os.Stdout {
				os.Stdout.Close()
			}

			os.Stdout = f
		}
	}

	fmt.Fprintln(os.Stderr, "output:", os.Stdout.Name())
	return
}

func (cmd *Cmd) command_exit(line string) (stop bool) {
	if !cmd.SilentResult() {
		fmt.Println("goodbye!")
	}
	return true
}

//
// This method executes one command
//
func (cmd *Cmd) oneCmd(line string) (stop bool) {
	if cmd.GetBoolVar("timing") {
		start := time.Now()
		defer func() {
			d := time.Since(start).Truncate(time.Millisecond)
			cmd.SetVar("elapsed", d.Seconds())

			if !cmd.SilentResult() {
				fmt.Println("Elapsed:", d)
			}
		}()
	}

	if cmd.GetBoolVar("echo") {
		fmt.Println(cmd.Prompt, line)
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
	}

	if command, ok := cmd.Commands[cname]; ok {
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
	if len(cmd.ContinuationPrompt) == 0 {
		cmd.ContinuationPrompt = ": "
	}

	cmd.context.StartLiner(cmd.HistoryFile)
	cmd.context.SetWordCompleter(cmd.wordCompleter)

	cmd.updateCompleters()
	cmd.PreLoop()

	defer func() {
		cmd.context.StopLiner()
		cmd.PostLoop()

		if os.Stdout != cmd.stdout {
			os.Stdout.Close()
			os.Stdout = cmd.stdout
		}
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)

	go func() {
		for sig := range sigc {
			cmd.setInterrupted(true)
			cmd.context.ResetTerminal()

			if cmd.Interrupt(sig) {
				// rethrow signal to kill app
				signal.Stop(sigc)
				p, _ := os.FindProcess(os.Getpid())
				p.Signal(sig)
			} else {
				//signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
			}
		}
	}()

	cmd.runLoop(true)
}

func (cmd *Cmd) runLoop(mainLoop bool) (stop bool) {
	// loop until ReadLine returns nil (signalling EOF)
	for {
		line, err := cmd.context.ReadLine(cmd.Prompt, cmd.ContinuationPrompt)
		if err != nil {
			if err != io.EOF {
				fmt.Println(err)
			}
			break
		}

		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		if mainLoop {
			cmd.setInterrupted(false)
			cmd.context.UpdateHistory(line) // allow user to recall this line
		}

		m, _ := cmd.context.TerminalMode()
		//interactive := err == nil

		cmd.PreCmd(line)
		stop = cmd.OneCmd(line)
		stop = cmd.PostCmd(line, stop) || (mainLoop == false && cmd.Interrupted())

		cmd.context.RestoreMode(m)
		if stop {
			break
		}
	}

	return
}

//
// RunBlock runs a block of code.
//
// Note: this is public because it's needed by the ControlFlow plugin (and can't be in interal
// because of circular dependencies). It shouldn't be used by end-user applications.
//
func (cmd *Cmd) RunBlock(name string, body []string, args []string, newscope bool) (stop bool) {
	if args != nil {
		args = append([]string{name}, args...)
	}

	prev := cmd.context.ScanBlock(body)
	if newscope {
		cmd.context.PushScope(nil, args)
	}
	shouldStop := cmd.runLoop(false)
	if newscope {
		cmd.context.PopScope()
	}
	cmd.context.SetScanner(prev)

	if name == "" { // if stop is called in an unamed block (i.e. not a function) we should really stop
		stop = shouldStop
	}

	return
}

//
// SetVar sets a variable in the current scope
//
func (cmd *Cmd) SetVar(k string, v interface{}) {
	cmd.context.SetVar(k, v, internal.LocalScope)
}

//
// UnsetVar removes a variable from the current scope
//
func (cmd *Cmd) UnsetVar(k string) {
	cmd.context.UnsetVar(k, internal.LocalScope)
}

//
// ChangeVar sets a variable in the current scope
// and calls the OnChange method
//
func (cmd *Cmd) ChangeVar(k string, v interface{}) {
	var oldv interface{} = NoVar
	if cur, ok := cmd.context.GetVar(k); ok {
		oldv = cur
	}
	if newv := cmd.OnChange(k, oldv, v); newv == NoVar {
		cmd.context.UnsetVar(k, internal.LocalScope)
	} else {
		cmd.context.SetVar(k, newv, internal.LocalScope)
	}
}

//
// GetVar return the value of the specified variable from the closest scope
//
func (cmd *Cmd) GetVar(k string) (string, bool) {
	return cmd.context.GetVar(k)
}

//
// GetBoolVar returns the value of the variable as boolean
//
func (cmd *Cmd) GetBoolVar(name string) (val bool) {
	sval, _ := cmd.context.GetVar(name)
	val, _ = strconv.ParseBool(sval)
	return
}

//
// GetIntVar returns the value of the variable as int
//
func (cmd *Cmd) GetIntVar(name string) (val int) {
	sval, _ := cmd.context.GetVar(name)
	val, _ = strconv.Atoi(sval)
	return
}

//
// SilentResult returns true if the command should be silent
// (not print results to the console, but only store in return variable)
//
func (cmd *Cmd) SilentResult() bool {
	return cmd.GetBoolVar("print") == false
}
