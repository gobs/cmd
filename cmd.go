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
	line         *liner.State   // interactive line reader
	scanner      *bufio.Scanner // file based line reader
	completer    *Completer
	commandNames []string
	functions    map[string][]string
	context      []arguments

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
	cmd.Add(Command{"var", `var name value`, cmd.Variable, nil})
	cmd.Add(Command{"if", `if (condition) body`, cmd.Conditional, nil})
	cmd.Add(Command{"load", `load script-file`, cmd.Load, nil})

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

	lines, _, err := cmd.readBlock(body, "")
	if err != nil {
		fmt.Println(err)
		return true
	}

	cmd.functions[fname] = lines
	// fmt.Println("function", fname, lines)
	return
}

func (cmd *Cmd) Variable(line string) (stop bool) {
	// var
	if line == "" {
		if len(cmd.Vars) == 0 {
			fmt.Println("no variables")
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

func (cmd *Cmd) Conditional(line string) (stop bool) {
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

	if res {
		stop = cmd.runBlock("", trueBlock, nil)
	} else {
		stop = cmd.runBlock("", falseBlock, nil)
	}

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

	cmd.setScanner(f)

	defer func() {
		cmd.setScanner(nil)
		f.Close()
	}()

	for {
		line, err = cmd.readLine("")
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

		stop = cmd.OneCmd(line) || cmd.stop
		if stop {
			break
		}
	}

	return stop
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

	if echo, _ := strconv.ParseBool(cmd.Vars["echo"]); echo {
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

	// loop until ReadLine returns nil (signalling EOF)
	for {
		line, err := cmd.readLine(cmd.Prompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			break
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
		stop = cmd.PostCmd(line, stop) || cmd.stop

		if m != nil {
			m.ApplyMode()
		}

		if stop {
			break
		}
	}
}

func (cmd *Cmd) setScanner(r io.Reader) {
	if r == nil {
		cmd.scanner = nil
	} else {
		cmd.scanner = bufio.NewScanner(r)
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
			fmt.Fprintln(os.Stderr, err)
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

func (cmd *Cmd) setContextVar(k, v string) {
	l := len(cmd.context)
	if l == 0 {
		panic("out of context")
	}

	cmd.context[l-1][k] = v
}

func (cmd *Cmd) getContextVar(k string) string {
	l := len(cmd.context)
	if l == 0 {
		panic("out of context")
	}

	for i := len(cmd.context) - 1; i >= 0; i-- {
		if v, ok := cmd.context[i][k]; ok {
			return v
		}
	}

	return ""
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

			if v, ok := cmd.Vars[arg]; ok {
				return v
			}

			return cmd.getContextVar(arg)
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
			if nargs != 1 {
				err = fmt.Errorf("expected 1 argument, got %v", nargs)
			} else {
				res = len(args[0]) == 0
			}
		case "n":
			if nargs != 1 {
				err = fmt.Errorf("expected 1 argument, got %v", nargs)
			} else {
				res = len(args[0]) != 0
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
	args = append([]string{name}, args...)
	cmd.pushContext(nil, args)

	for _, line := range body {
		if strings.HasPrefix(line, "#") || line == "" {
			cmd.EmptyLine()
			continue
		}

		stop = cmd.OneCmd(line) || cmd.stop
		if stop {
			break
		}
	}

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

		if strings.HasPrefix(line, "}") { // close first block
			break
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

		if strings.HasPrefix(line, "}") { // close first block
			break
		}

		block2 = append(block2, line)
	}

	return block1, block2, nil
}
