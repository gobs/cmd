package controlflow

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gobs/args"
	"github.com/gobs/cmd"
	"github.com/gobs/cmd/internal"
	"github.com/gobs/simplejson"
)

type controlFlow struct {
	cmd.Plugin

	cmd *cmd.Cmd
	ctx *internal.Context

	runCmd func(string) bool

	functions     map[string][]string
	functionNames []string
}

var (
	Plugin = &controlFlow{}

	reArg       = regexp.MustCompile(`\$(\w+|\(\w+\)|\(env.\w+\)|[\*#]|\([\*#]\))`) // $var or $(var)
	reVarAssign = regexp.MustCompile(`([\d\w]+)(=(.*))?`)                           // name=value
)

func (cf *controlFlow) updateCompleter() {
	cf.functionNames = cf.functionNames[:0]
	for name := range cf.functions {
		cf.functionNames = append(cf.functionNames, name)
	}
	sort.Strings(cf.functionNames)

	c := cf.cmd.GetCompleter("function")
	if c == nil {
		cf.cmd.AddCompleter("function", cmd.NewWordCompleter(cf.functionNames, func(s, l string) bool {
			return strings.HasPrefix(l, "function ")
		}))
	} else {
		c.(*cmd.WordCompleter).Words = cf.functionNames
	}
}

func (cf *controlFlow) command_function(line string) (stop bool) {
	// function
	if line == "" {
		if len(cf.functions) == 0 {
			fmt.Println("no functions")
		} else {
			fmt.Println("functions:")
			for _, fn := range cf.functionNames {
				fmt.Println(" ", fn)
			}
		}
		return
	}

	parts := strings.SplitN(line, " ", 2)
	// function name
	if len(parts) == 1 {
		fn := parts[0]
		body, ok := cf.functions[fn]
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
		if _, ok := cf.functions[fname]; ok {
			delete(cf.functions, fname)
			fmt.Println("function", fname, "deleted")

			cf.updateCompleter()
		} else {
			fmt.Println("no function", fname)
		}

		return
	}

	lines, _, err := cf.ctx.ReadBlock(body, "", cf.cmd.ContinuationPrompt)
	if err != nil {
		fmt.Println(err)
		return true
	}

	cf.functions[fname] = lines
	cf.updateCompleter()
	return
}

func (cf *controlFlow) command_variable(line string) (stop bool) {
	options, line := args.GetOptions(line)

	var remove bool
	var scope internal.Scope

	for _, op := range options {
		switch op {
		case "-g", "--global":
			scope = internal.GlobalScope

		case "-p", "--parent", "--return":
			scope = internal.ParentScope

		case "-r", "-rm", "--remove", "-u", "--unset":
			remove = true

		default:
			fmt.Printf("invalid option -%v\n", op)
			return
		}
	}

	// var
	if len(line) == 0 {
		if scope != internal.InvalidScope {
			fmt.Printf("invalid use of %v scope option", scope)
			return
		}

		for k, v := range cf.ctx.GetAllVars() {
			fmt.Printf("  %v=%v\n", k, v)
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
		cf.ctx.SetVar(name, parts[1], scope)
		return
	}

	// var -r name
	if remove {
		cf.ctx.UnsetVar(name, scope)
		return
	}

	// var name
	if scope != internal.InvalidScope {
		fmt.Printf("invalid use of %v scope option", scope)
		return
	}

	value, ok := cf.ctx.GetVar(name)
	if ok {
		fmt.Println(name, "=", value)
	}
	return
}

func (cf *controlFlow) expandVariables(line string) string {
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

			v, _ := cf.ctx.GetVar(arg)
			return v
		})

		// fmt.Println("after expand:", line)
		if !found {
			break
		}
	}

	return line
}

func (cf *controlFlow) command_conditional(line string) (stop bool) {
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

	res, err := cf.evalConditional(parts[0])
	if err != nil {
		fmt.Println(err)
		return true
	}

	trueBlock, falseBlock, err := cf.ctx.ReadBlock(parts[1], "else", cf.cmd.ContinuationPrompt)
	if err != nil {
		fmt.Println(err)
		return true
	}

	if negate {
		res = !res
	}

	if res {
		stop = cf.cmd.RunBlock("", trueBlock, nil)
	} else {
		stop = cf.cmd.RunBlock("", falseBlock, nil)
	}

	return
}

func compare(args []string, num bool) (int, error) {
	l := len(args)

	if l > 2 || (num && l != 2) {
		return 0, fmt.Errorf("expected 2 arguments, got %v", len(args))
	}

	var arg1, arg2 string

	if l > 0 {
		arg1 = args[0]
	}
	if l > 1 {
		arg2 = args[1]
	}

	if num {
		n1, _ := parseInt(arg1)
		n2, _ := parseInt(arg2)
		return n1 - n2, nil
	} else {
		return strings.Compare(arg1, arg2), nil
	}
}

func (cf *controlFlow) evalConditional(line string) (res bool, err error) {
	if strings.HasPrefix(line, "(") && strings.HasSuffix(line, ")") { // (condition arg1 arg2...)
		line = line[1:]
		line = line[:len(line)-1]
		args := args.GetArgs(line)
		if len(args) == 0 {
			return false, fmt.Errorf("invalid condition: %q", line)
		}

		cond, args := args[0], args[1:]
		nargs := len(args)

		var cres int

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
				res = true
			}
		case "eq":
			cres, err = compare(args, false)
			res = cres == 0
		case "ne":
			cres, err = compare(args, false)
			res = cres != 0
		case "gt":
			cres, err = compare(args, false)
			res = cres > 0
		case "gte":
			cres, err = compare(args, false)
			res = cres >= 0
		case "lt":
			cres, err = compare(args, false)
			res = cres < 0
		case "lte":
			cres, err = compare(args, false)
			res = cres <= 0
		case "eq#":
			cres, err = compare(args, true)
			res = cres == 0
		case "ne#":
			cres, err = compare(args, true)
			res = cres != 0
		case "gt#":
			cres, err = compare(args, true)
			res = cres > 0
		case "gte#":
			cres, err = compare(args, true)
			res = cres >= 0
		case "lt#":
			cres, err = compare(args, true)
			res = cres < 0
		case "lte#":
			cres, err = compare(args, true)
			res = cres <= 0
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

func floatString(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	return strings.TrimSuffix(s, ".000")
}

func (cf *controlFlow) command_expression(line string) (stop bool) {
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

		n1, err := parseFloat(parts[0])
		if err != nil {
			fmt.Println("not a number:", parts[0])
			return
		}

		n2, err := parseFloat(parts[1])
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
		res = floatString(n1)

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

	if !cf.cmd.SilentResult() {
		fmt.Println(res)
	}

	cf.cmd.SetVar("result", res)
	return
}

func getList(line string) []interface{} {
	if strings.HasPrefix(line, "[") {
		j, err := simplejson.LoadString(line)
		if err == nil {
			return j.MustArray()
		}

		line = line[1:]
		if strings.HasSuffix(line, "]") {
			line = line[:len(line)-1]
		}
	} else if strings.HasPrefix(line, "(") {
		line = line[1:]
		if strings.HasSuffix(line, ")") {
			line = line[:len(line)-1]
		}
	}

	arr := args.GetArgs(line)
	iarr := make([]interface{}, len(arr))
	for i, v := range arr {
		iarr[i] = v
	}
	return iarr
}

func (cf *controlFlow) command_repeat(line string) (stop bool) {
	forever := ^uint64(0) // almost forever

	count := forever
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
				arg = cf.expandVariables(arg)
				count, _ = strconv.ParseUint(arg[8:], 10, 64)
			} else if strings.HasPrefix(arg, "--wait=") {
				arg = cf.expandVariables(arg)
				w, err := strconv.Atoi(arg[7:])
				if err == nil {
					wait = time.Duration(w) * time.Second
				} else {
					wait, _ = time.ParseDuration(arg[7:])
				}
			} else {
				// unknown option
				fmt.Println("invalid option", arg)
				return
			}
		} else {
			break
		}
	}

	block, _, err := cf.ctx.ReadBlock(line, "", cf.cmd.ContinuationPrompt)
	if err != nil {
		fmt.Println(err)
		return
	}

	cf.ctx.PushScope(nil, nil)
	cf.cmd.SetVar("count", count)

	for i := uint64(1); i <= count; i++ {
		if wait > 0 && i > 0 {
			time.Sleep(wait)
		}

		cf.cmd.SetVar("index", i)
		rstop := cf.cmd.RunBlock("", block, nil) || cf.cmd.Interrupted
		if rstop {
			break
		}
	}

	cf.ctx.PopScope()
	return
}

func (cf *controlFlow) command_foreach(line string) (stop bool) {
	arg := ""
	wait := time.Duration(0) // no wait

	for {
		if strings.HasPrefix(line, "-") {
			// some options
			parts := strings.SplitN(line, " ", 2)
			if len(parts) < 2 {
				// no command
				return
			}

			arg, line = parts[0], strings.TrimSpace(parts[1])
			if arg == "--" {
				break
			}

			if strings.HasPrefix(arg, "--wait=") {
				arg = cf.expandVariables(arg)

				w, err := strconv.Atoi(arg[7:])
				if err == nil {
					wait = time.Duration(w) * time.Second
				} else {
					wait, _ = time.ParseDuration(arg[7:])
				}
			} else {
				// unknown option
				fmt.Println("invalid option", arg)
				return
			}
		} else {
			break
		}
	}

	parts := args.GetArgsN(line, 2) // [ list, command ]
	if len(parts) != 2 {
		fmt.Println("missing argument(s)")
		return
	}

	list, command := cf.expandVariables(parts[0]), parts[1]

	args := getList(list)
	count := len(args)

	block, _, err := cf.ctx.ReadBlock(command, "", cf.cmd.ContinuationPrompt)
	if err != nil {
		fmt.Println(err)
		return
	}

	cf.ctx.PushScope(nil, nil)
	cf.cmd.SetVar("count", count)

	for i, v := range args {
		if wait > 0 && i > 0 {
			time.Sleep(wait)
		}

		cf.cmd.SetVar("index", i)
		cf.cmd.SetVar("item", v)
		rstop := cf.cmd.RunBlock("", block, nil) || cf.cmd.Interrupted
		if rstop {
			break
		}
	}

	cf.ctx.PopScope()
	return
}

func (cf *controlFlow) command_load(line string) (stop bool) {
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

	prev := cf.ctx.ScanReader(f)

	defer func() {
		cf.ctx.SetScanner(prev)
		f.Close()
	}()

	for {
		line, err = cf.ctx.ReadLine("load", "")
		if err != nil {
			if err != io.EOF {
				fmt.Println(err)
			}
			break
		}

		if strings.HasPrefix(line, "#") || line == "" {
			cf.cmd.EmptyLine()
			continue
		}

		// fmt.Println("load-one", line)
		if cf.cmd.OneCmd(line) || cf.cmd.Interrupted {
			break
		}
	}

	return
}

func (cf *controlFlow) command_stop(_o string) (stop bool) {
	return true
}

// XXX: don't expand one-line body of "function" or "repeat"
func canExpand(line string) bool {
	if strings.HasPrefix(line, "function ") {
		return false
	}
	if strings.HasPrefix(line, "repeat ") {
		return false
	}
	if strings.HasPrefix(line, "foreach ") {
		return false
	}
	return true
}

func (cf *controlFlow) runFunction(line string) bool {
	if canExpand(line) {
		line = cf.expandVariables(line)
	}

	if cf.cmd.GetBoolVar("echo") {
		fmt.Println(cf.cmd.Prompt, line)
	}

	if strings.HasPrefix(line, "@") {
		line = "load " + line[1:]
	} else {
		parts := strings.SplitN(line, " ", 2)

		cname, params := parts[0], ""
		if len(parts) > 1 {
			params = strings.TrimSpace(parts[1])
		}

		if function, ok := cf.functions[cname]; ok {
			return cf.cmd.RunBlock(cname, function, args.GetArgs(params))
		}
	}

	return cf.runCmd(line)
}

//
// PluginInit initialize this plugin
//
func (cf *controlFlow) PluginInit(c *cmd.Cmd, ctx *internal.Context) error {
	if cf.cmd != nil {
		return nil // already initialized
	}

	rand.Seed(time.Now().Unix())

	cf.cmd, cf.ctx = c, ctx
	cf.runCmd, c.OneCmd = c.OneCmd, cf.runFunction
	cf.functions = make(map[string][]string)
	cf.functionNames = make([]string, 0)

	c.Add(cmd.Command{"function", `function name body`, cf.command_function, nil})
	c.Add(cmd.Command{"var", `var [-g|--global] [-r|--remove|-u|--unset] name value`, cf.command_variable, nil})
	c.Add(cmd.Command{"if", `if (condition) command`, cf.command_conditional, nil})
	c.Add(cmd.Command{"expr", `expr operator operands...`, cf.command_expression, nil})
	c.Add(cmd.Command{"foreach", `foreach [--wait=duration] (items...) command`, cf.command_foreach, nil})
	c.Add(cmd.Command{"repeat", `repeat [--count=n] [--wait=duration] [--echo] command`, cf.command_repeat, nil})
	c.Add(cmd.Command{"load", `load script-file`, cf.command_load, nil})
	c.Add(cmd.Command{"stop", `stop function or block`, cf.command_stop, nil})

	c.Commands["set"] = c.Commands["var"]
	return nil
}
