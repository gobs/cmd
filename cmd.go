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

	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand"
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
	reArg       = regexp.MustCompile(`\$(\w+|\(\w+\)|\(env.\w+\)|[\*#]|\([\*#]\))`) // $var or $(var)
	reVarAssign = regexp.MustCompile(`([\d\w]+)(=(.*))?`)                           // name=value
	sep         = string(0xFFFD)                                                    // unicode replacement char
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
// A basic scanner interface
//
type basicScanner interface {
	Scan() bool
	Text() string
	Err() error
}

//
// An implementation of basicScanner that works on a list of lines
//
type scanLines struct {
	lines []string
}

func (s *scanLines) Scan() bool {
	return len(s.lines) > 0
}

func (s *scanLines) Text() (text string) {
	if len(s.lines) == 0 {
		return
	}

	text, s.lines = s.lines[0], s.lines[1:]
	return
}

func (s *scanLines) Err() (err error) {
	return
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
	Vars arguments

	///////// private stuff /////////////
	line              *liner.State // interactive line reader
	scanner           basicScanner // file based line reader
	commandNames      []string
	functions         map[string][]string
	functionNames     []string
	context           []arguments
	commandCompleter  *Completer
	functionCompleter *Completer

	waitGroup          *sync.WaitGroup
	waitMax, waitCount int
	stop               bool
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
	cmd.Add(Command{"var", `var [-l|--local] [-q|--quiet] [-r|--remove] name value`, cmd.Variable, nil})
	cmd.Add(Command{"if", `if (condition) body`, cmd.Conditional, nil})
	cmd.Add(Command{"expr", `expr operator operands...`, cmd.Expression, nil})
	cmd.Add(Command{"load", `load script-file`, cmd.Load, nil})

	cmd.functions = make(map[string][]string)

	rand.Seed(time.Now().Unix())
}

func (cmd *Cmd) SetPrompt(prompt string, max int) {
	l := len(prompt)

	if max > 3 && l > max {
		max -= 3 // for "..."
		prompt = "..." + prompt[l-max:]
	}

	cmd.Prompt = prompt
}

// SetVar set a context variable (in cmd.Vars)
func (cmd *Cmd) SetVar(name string, value interface{}, local bool) {
	vars := cmd.Vars

	if local && cmd.getContext() != nil {
		vars = cmd.getContext()
	}

	if value == nil {
		if _, ok := vars[name]; ok {
			delete(vars, name)
		}
		return
	}

	vars[name] = fmt.Sprintf("%v", value)
}

// GetVar returns the value of the context variable (from cmd.Vars) as string
func (cmd *Cmd) GetVar(name string, local bool) (val string) {
	if local {
		if v, ok := cmd.getContextVar(name); ok {
			return v
		}
	}

	if v, ok := cmd.Vars[name]; ok {
		return v
	}

	return
}

// GetBoolVar returns the value of the context variable (from cmd.Vars) as bool
func (cmd *Cmd) GetBoolVar(name string, local bool) (val bool) {
	val, _ = strconv.ParseBool(cmd.GetVar(name, local))
	return
}

// GetIntVar returns the value of the context variable (from cmd.Vars) as int
func (cmd *Cmd) GetIntVar(name string, local bool) (val int) {
	val, _ = strconv.Atoi(cmd.GetVar(name, local))
	return
}

//
// Update function completer (when function list changes)
//
func (cmd *Cmd) updateCompleters() {
	if cmd.commandCompleter == nil {
		cmd.commandNames = make([]string, 0, len(cmd.Commands))
		for name := range cmd.Commands {
			cmd.commandNames = append(cmd.commandNames, name)
		}
		sort.Strings(cmd.commandNames) // for help listing

		cmd.commandCompleter = NewCompleter(cmd.commandNames)
	}

	if cmd.functionCompleter == nil {
		cmd.functionNames = []string{}
		cmd.functionCompleter = NewCompleter(cmd.functionNames)
	}

	cmd.functionNames = cmd.functionNames[:0]
	for name := range cmd.functions {
		cmd.functionNames = append(cmd.functionNames, name)
	}
	sort.Strings(cmd.functionNames) // for function listing
	cmd.functionCompleter.Words = cmd.functionNames

	cmd.commandCompleter.Words = cmd.commandCompleter.Words[:0]
	cmd.commandCompleter.Words = append(cmd.commandCompleter.Words, cmd.commandNames...)
	cmd.commandCompleter.Words = append(cmd.commandCompleter.Words, cmd.functionNames...)
	sort.Strings(cmd.commandCompleter.Words)
}

func (cmd *Cmd) wordCompleter(line string, pos int) (head string, completions []string, tail string) {
	start := strings.LastIndex(line[:pos], " ")
	if start < 0 { // this is the command to match
		return "", cmd.commandCompleter.Complete(line), line[pos:]
	} else if strings.HasPrefix(line, "help ") {
		return line[:start+1], cmd.commandCompleter.Complete(line[start+1:]), line[pos:]
	} else if strings.HasPrefix(line, "function ") {
		return line[:start+1], cmd.functionCompleter.Complete(line[start+1:]), line[pos:]
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

		if len(cmd.functionNames) > 0 {
			fmt.Println()
			fmt.Println("Available functions:")
			fmt.Println("================================================================")

			tp := pretty.NewTabPrinter(8)
			for _, c := range cmd.functionNames {
				tp.Print(c)
			}
			tp.Println()
		}
	} else {
		if c, ok := cmd.Commands[line]; ok {
			c.HelpFunc()
		} else if _, ok := cmd.functions[line]; ok {
			fmt.Println(line, "is a function")
		} else {
			fmt.Println("unknown command or function")
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
	count := ^uint64(0)      // almost forever
	wait := time.Duration(0) // no wait
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

			if strings.HasPrefix(arg, "--count=") {
				count, _ = strconv.ParseUint(arg[8:], 10, 64)
				fmt.Println("count", count)
			} else if strings.HasPrefix(arg, "--wait=") {
				w, err := strconv.Atoi(arg[7:])
				if err == nil {
					wait = time.Duration(w) * time.Second
				} else {
					wait, _ = time.ParseDuration(arg[7:])
				}
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

	block, _, err := cmd.readBlock(line, "")
	if err != nil {
		fmt.Println(err)
		return
	}

	cmd.pushContext(arguments{"count": strconv.FormatUint(count, 10)}, nil)

	for i := uint64(1); i <= count; i++ {
		cmd.setContextVar("index", strconv.FormatUint(i, 10))
		stop = cmd.runBlock("", block, nil) || cmd.stop
		if stop {
			break
		}

		if wait > 0 && i < count-1 {
			time.Sleep(wait)
		}
	}

	cmd.popContext()
	return
}

func (cmd *Cmd) Function(line string) (stop bool) {
	// function
	if line == "" {
		if len(cmd.functions) == 0 {
			fmt.Println("no functions")
		} else {
			fmt.Println("functions:")
			for _, fn := range cmd.functionNames {
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
			cmd.updateCompleters()
			fmt.Println("function", fname, "deleted")
		} else {
			fmt.Println("no function", fname)
		}

		return
	}

	lines, _, err := cmd.readBlock(body, "")
	if err != nil {
		fmt.Println(err)
		return true
	}

	cmd.functions[fname] = lines
	cmd.updateCompleters()
	return
}

func (cmd *Cmd) Variable(line string) (stop bool) {
	options, line := args.GetOptions(line)

	var quiet bool
	var remove bool

	prefix := "global"
	vars := cmd.Vars

	for _, op := range options {
		switch op {
		case "-q", "--quiet":
			quiet = true

		case "-l", "--local":
			if cmd.getContext() != nil {
				vars = cmd.getContext()
				prefix = "local"
			}

		case "-r", "-rm", "--remove":
			remove = true

		default:
			fmt.Printf("invalid option -%v\n", op)
			return
		}
	}

	// var
	if len(line) == 0 {
		if len(vars) == 0 {
			fmt.Println("no", prefix, "variables")
		} else {
			fmt.Println(prefix, "variables:")
			for k, v := range vars {
				fmt.Println(" ", k, "=", v)
			}
		}

		return
	}

	parts := args.GetArgsN(line, 2) // [ name, value ]
	if len(parts) == 1 {            // see if it's name=value
		matches := reVarAssign.FindStringSubmatch(line)
		if len(matches) > 0 { // [name=var name =var var]
			parts = []string{matches[1], matches[3]}
		}
	}

	name := parts[0]

	// var name value
	if len(parts) == 2 {
		if vars == nil {
			fmt.Println(prefix, "is nil")
		}
		vars[name] = parts[1]
		if quiet {
			return
		}
	}

	value, ok := vars[name]

	if remove {
		if ok {
			delete(vars, name)

			if !quiet {
				fmt.Println(name, "removed")
			}

			return
		}
	}

	if !ok {
		if !quiet {
			fmt.Println("no", prefix, "variable", name)
		}
	} else {
		fmt.Println(name, "=", value)
	}
	return
}

func (cmd *Cmd) Conditional(line string) (stop bool) {
	negate := false

	if strings.HasPrefix(line, "!") { // negate condition
		negate = true
		line = line[1:]
	}

	if len(line) == 0 {
		fmt.Println("missing condition")
		return
	}

	parts := args.GetArgsN(line, 2) // [ condition, body ]
	if len(parts) != 2 {
		fmt.Println("missing body")
		return
	}

	res, err := cmd.evalConditional(parts[0])
	if err != nil {
		fmt.Println(err)
		return true
	}

	trueBlock, falseBlock, err := cmd.readBlock(parts[1], "else")
	if err != nil {
		fmt.Println(err)
		return true
	}

	if negate {
		res = !res
	}

	if res {
		stop = cmd.runBlock("", trueBlock, nil)
	} else {
		stop = cmd.runBlock("", falseBlock, nil)
	}

	return
}

func parseInt64(v string) (int64, error) {
	return strconv.ParseInt(v, 10, 64)
}

func parseInt(v string) (int, error) {
	i, err := strconv.ParseInt(v, 10, 64)
	return int(i), err
}

func parseFloat(v string) (float64, error) {
	return strconv.ParseFloat(v, 64)
}

func intString(v int64, base int) string {
	if base == 0 {
		base = 10
	}

	return strconv.FormatInt(v, base)
}

func (cmd *Cmd) Expression(line string) (stop bool) {
	parts := args.GetArgsN(line, 2) // [ op, arg1 ]
	if len(parts) != 2 {
		fmt.Println("missing argument(s)")
		return
	}

	op, line := parts[0], parts[1]

	var res interface{}

	switch op {
	case "rand":
		parts := args.GetArgs(line) // [ max, base ]
		if len(parts) > 2 {
			fmt.Println("usage: rand max [base]")
			return
		}

		neg := false
		base := 10

		max, err := parseInt64(parts[0])
		if err != nil || max == 0 {
			max = math.MaxInt64
		} else if max < 0 {
			neg = true
			max = -max
		}

		if len(parts) == 2 {
			base, err = parseInt(parts[1])
			if err != nil {
				fmt.Println("base should be a number")
				return
			}

			if base <= 0 {
				base = 10
			} else if base > 36 {
				base = 36
			}
		}

		r := rand.Int63n(max)
		if neg {
			r = -r
		}
		res = intString(r, base)

	case "+", "-", "*", "/":
		parts := args.GetArgs(line) // [ arg1, arg2 ]
		if len(parts) != 2 {
			fmt.Println("usage:", op, "arg1 arg2")
			return
		}

		n1, err := parseInt64(parts[0])
		if err != nil {
			fmt.Println("not a number:", parts[0])
			return
		}

		n2, err := parseInt64(parts[1])
		if err != nil {
			fmt.Println("not a number:", parts[1])
			return
		}

		if op == "+" {
			n1 += n2
		} else if op == "-" {
			n1 -= n2
		} else if op == "*" {
			n1 *= n2
		} else if op == "/" {
			n1 /= n2
		}
		res = intString(n1, 10)

	case "upper":
		res = strings.ToUpper(line)

	case "lower":
		res = strings.ToLower(line)

	case "substr":
		parts := args.GetArgsN(line, 2) // [ start:end, line ]
		if len(parts) == 0 {
			fmt.Println("usage: substr start:end line")
			return
		}

		if len(parts) == 1 { // empty line ?
			line = ""
		} else {
			line = parts[1]
		}

		srange := parts[0]
		var start, end int

		if !strings.Contains(srange, ":") {
			fmt.Println("expected start:end, got", srange)
			return
		}

		parts = strings.Split(srange, ":")

		start, _ = parseInt(parts[0])
		if start < 0 {
			start = len(line) + start
		}
		if start < 0 {
			start = 0
		} else if start > len(line) {
			start = len(line)
		}

		if parts[1] == "" { // start:
			end = len(line)
		} else {
			end, _ = parseInt(parts[1])
		}

		if end < 0 {
			end = start + len(line) + end
		}

		if end < start {
			end = start
		} else if end > len(line) {
			end = len(line)
		}

		res = line[start:end]

	default:
		fmt.Println("invalid operator:", op)
		return
	}

	fmt.Println(res)
	cmd.SetVar("result", res, false)
	return
}

func (cmd *Cmd) Load(line string) (stop bool) {
	if len(line) == 0 {
		fmt.Println("missing script file")
		return
	}

	fname := line
	f, err := os.Open(fname)
	if err != nil {
		fmt.Println(err)
		return
	}

	cmd.scanner = bufio.NewScanner(f)

	defer func() {
		cmd.scanner = nil
		f.Close()
	}()

	for {
		line, err = cmd.readLine("load")
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

		// fmt.Println("load-one", line)
		if cmd.OneCmd(line) || cmd.stop {
			break
		}
	}

	return
}

// XXX: don't expand one-line body of "function" or "repeat"
func canExpand(line string) bool {
	if strings.HasPrefix(line, "function ") {
		return false
	}
	if strings.HasPrefix(line, "repeat ") {
		return false
	}
	return true
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

	if canExpand(line) {
		line = cmd.expandVariables(line)
	}

	if cmd.GetBoolVar("echo", true) {
		fmt.Println(cmd.Prompt, line)
	}

	if cmd.EnableShell && strings.HasPrefix(line, "!") {
		shellExec(line[1:])
		return
	}

	var cname, params string

	if strings.HasPrefix(line, "@") {
		cname = "load"
		params = strings.TrimSpace(line[1:])
	} else {
		parts := strings.SplitN(line, " ", 2)

		cname = parts[0]
		if len(parts) > 1 {
			params = strings.TrimSpace(parts[1])
		}
	}

	if command, ok := cmd.Commands[cname]; ok {
		stop = command.Call(params)
	} else if function, ok := cmd.functions[cname]; ok {
		stop = cmd.runBlock(cname, function, args.GetArgs(params))
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

	cmd.updateCompleters()

	if cmd.line == nil {
		cmd.line = liner.NewLiner()
		cmd.line.SetWordCompleter(cmd.wordCompleter)
		cmd.readHistoryFile()
	}
	// cmd.line.SetCtrlCAborts(cmd.CtrlCAborts)

	cmd.PreLoop()

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

		cmd.stop = true

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

	cmd.runLoop(true)
}

func (cmd *Cmd) runLoop(updateHistory bool) {
	// loop until ReadLine returns nil (signalling EOF)
	for {
		line, err := cmd.readLine(cmd.Prompt)
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

		if cmd.line != nil && updateHistory {
			cmd.line.AppendHistory(line) // allow user to recall this line
		}

		m, _ := liner.TerminalMode()

		cmd.PreCmd(line)
		stop := cmd.OneCmd(line)
		stop = cmd.PostCmd(line, stop) || cmd.stop

		if m != nil {
			m.ApplyMode()
		}

		if stop {
			break
		}
	}
}

func (cmd *Cmd) readOneLine(prompt string) (line string, err error) {
	if cmd.scanner != nil {
		if cmd.scanner.Scan() {
			line = cmd.scanner.Text()
		} else if cmd.scanner.Err() != nil {
			err = cmd.scanner.Err()
		} else {
			err = io.EOF
		}
	} else {
		line, err = cmd.line.Prompt(prompt)
	}

	// fmt.Printf("readOneLine:%v %q %v\n", prompt, line, err)
	return
}

func (cmd *Cmd) readLine(prompt string) (line string, err error) {
	line, err = cmd.readOneLine(prompt)
	if err != nil {
		return
	}

	line = strings.TrimSpace(line)

	//
	// merge lines ending with '\' into one single line
	//
	for strings.HasSuffix(line, "\\") { // continuation
		line = strings.TrimRight(line, "\\")
		line = strings.TrimSpace(line)

		l, err := cmd.readOneLine(cmd.ContinuationPrompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "continuation", err)
			break
		}

		line += " " + strings.TrimSpace(l)
	}

	return
}

func (cmd *Cmd) pushContext(vars map[string]string, args []string) {
	ctx := make(arguments)

	for k, v := range vars {
		ctx[k] = v
	}

	for i, v := range args {
		k := strconv.Itoa(i)
		ctx[k] = v
	}

	if args != nil {
		ctx["*"] = strings.Join(args[1:], " ") // all args
		ctx["#"] = strconv.Itoa(len(args[1:])) // args[0] is the function name
	}

	cmd.context = append(cmd.context, ctx)
}

func (cmd *Cmd) popContext() {
	l := len(cmd.context)
	if l == 0 {
		panic("out of context")
	}

	cmd.context = cmd.context[:l-1]
}

func (cmd *Cmd) getContext() arguments {
	l := len(cmd.context)
	if l == 0 {
		return nil
	}

	return cmd.context[l-1]
}

func (cmd *Cmd) setContextVar(k, v string) {
	l := len(cmd.context)
	if l == 0 {
		panic("out of context")
	}

	cmd.context[l-1][k] = v
}

func (cmd *Cmd) getContextVar(k string) (string, bool) {
	l := len(cmd.context)
	if l == 0 {
		return "", false
	}

	for i := len(cmd.context) - 1; i >= 0; i-- {
		if v, ok := cmd.context[i][k]; ok {
			return v, true
		}
	}

	return "", false
}

func (cmd *Cmd) expandVariables(line string) string {
	for {
		// fmt.Println("before expand:", line)
		found := false

		line = reArg.ReplaceAllStringFunc(line, func(s string) string {
			found = true

			// ReplaceAll doesn't return submatches so we need to cleanup
			arg := strings.TrimLeft(s, "$(")
			arg = strings.TrimRight(arg, ")")

			if strings.HasPrefix(arg, "env.") {
				return os.Getenv(arg[4:])
			}

			return cmd.GetVar(arg, true)
		})

		// fmt.Println("after expand:", line)
		if !found {
			break
		}
	}

	return line
}

func (cmd *Cmd) evalConditional(line string) (res bool, err error) {
	if strings.HasPrefix(line, "(") && strings.HasSuffix(line, ")") { // (condition arg1 arg2...)
		line = strings.TrimPrefix(line, "(")
		line = strings.TrimSuffix(line, ")")
		args := args.GetArgs(line)
		if len(args) == 0 {
			return false, fmt.Errorf("invalid condition: %q", line)
		}

		cond, args := args[0], args[1:]
		nargs := len(args)

		switch cond {
		case "z":
			switch nargs {
			case 0:
				res = true

			case 1:
				res = len(args[0]) == 0

			default:
				err = fmt.Errorf("expected 1 argument, got %v", nargs)
			}
		case "n":
			switch nargs {
			case 0:
				res = false

			case 1:
				res = len(args[0]) != 0

			default:
				err = fmt.Errorf("expected 1 argument, got %v", nargs)
			}
		case "eq":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] == args[1]
			}
		case "ne":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] != args[1]
			}
		case "gt":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] > args[1]
			}
		case "gte":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] >= args[1]
			}
		case "lt":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] < args[1]
			}
		case "lte":
			if nargs != 2 {
				err = fmt.Errorf("expected 2 argument, got %v", nargs)
			} else {
				res = args[0] <= args[1]
			}
		case "startswith":
			switch nargs {
			case 0:
				err = fmt.Errorf("expected 2 argument, got 0")

			case 1:
				res = false

			case 2:
				res = strings.HasPrefix(args[1], args[0])
			}
		case "endswith":
			switch nargs {
			case 0:
				err = fmt.Errorf("expected 2 argument, got 0")

			case 1:
				res = false

			case 2:
				res = strings.HasSuffix(args[1], args[0])
			}
		case "contains":
			switch nargs {
			case 0:
				err = fmt.Errorf("expected 2 argument, got 0")

			case 1:
				res = false

			case 2:
				res = strings.Contains(args[1], args[0])
			}
		default:
			err = fmt.Errorf("invalid condition: %q", line)
		}
	} else if len(line) == 0 || line == "0" {
		res = false
	} else {
		res = true
	}

	return
}

func (cmd *Cmd) runBlock(name string, body []string, args []string) (stop bool) {
	if args != nil {
		args = append([]string{name}, args...)
	}

	cmd.pushContext(nil, args)
	prev := cmd.scanner
	cmd.scanner = &scanLines{body}
	cmd.runLoop(false)
	cmd.scanner = prev
	cmd.popContext()
	return
}

func (cmd *Cmd) readBlock(body, next string) ([]string, []string, error) {
	if !strings.HasSuffix(body, "{") { // one line body
		body := strings.Replace(body, "\\$", "$", -1) // for one-liners variables should be escaped
		return []string{body}, nil, nil
	}

	if body != "{" { // we can't do inline command + body
		return nil, nil, fmt.Errorf("unexpected body and block")
	}

	opened := 1
	var block1, block2 []string
	var line string
	var err error

	for {

		line, err = cmd.readLine(cmd.ContinuationPrompt)
		if err != nil {
			return nil, nil, err
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		if strings.HasPrefix(line, "}") {
			opened -= 1
			if opened <= 0 { // close first block
				break
			}
		}
		if strings.HasSuffix(line, "{") {
			opened += 1
		}

		block1 = append(block1, line)
	}

	line = strings.TrimPrefix(line, "}")
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "#") || line == "" {
		return block1, nil, nil
	}

	if next != "" && !strings.HasPrefix(line, next) {
		return nil, nil, fmt.Errorf("expected %q, got %q", next, line)
	}

	line = line[len(next):]
	line = strings.TrimSpace(line)

	if line != "{" {
		return nil, nil, fmt.Errorf("expected }, got %q", line)
	}

	opened = 1

	for {

		line, err = cmd.readLine(cmd.ContinuationPrompt)
		if err != nil {
			return nil, nil, err
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		if strings.HasPrefix(line, "}") {
			opened -= 1
			if opened <= 0 { // close second block
				break
			}
		}
		if strings.HasSuffix(line, "{") {
			opened += 1
		}

		block2 = append(block2, line)
	}

	return block1, block2, nil
}
