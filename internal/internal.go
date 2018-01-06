package internal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/peterh/liner"
)

type Arguments = map[string]string

type Context struct {
	line    *liner.State // interactive line reader
	scanner BasicScanner // file based line reader

	historyFile string
	scopes      []Arguments
}

func NewContext(history string) *Context {
	ctx := &Context{}

	ctx.line = liner.NewLiner()
	ctx.readHistoryFile(history)
	ctx.ScanLiner()
	return ctx
}

func (ctx *Context) Close() {
	if ctx.line != nil {
		ctx.writeHistoryFile()
		ctx.line.Close()
	}
}

func (ctx *Context) UpdateHistory(line string) {
	if ctx.line != nil {
		ctx.line.AppendHistory(line)
	}
}

func (ctx *Context) SetWordCompleter(completer func(line string, pos int) (head string, completions []string, tail string)) {
	if ctx.line != nil {
		ctx.line.SetWordCompleter(completer)
	}
}

func (ctx *Context) readHistoryFile(history string) {
	if len(history) == 0 {
		// no history file
	}

	filepath := history // start with current directory
	if f, err := os.Open(filepath); err == nil {
		ctx.line.ReadHistory(f)
		f.Close()

		ctx.historyFile = filepath
	}

	filepath = path.Join(os.Getenv("HOME"), filepath) // then check home directory
	if f, err := os.Open(filepath); err == nil {
		ctx.line.ReadHistory(f)
		f.Close()

		ctx.historyFile = filepath
	}
}

func (ctx *Context) writeHistoryFile() {
	if len(ctx.historyFile) == 0 {
		// no history file
		return
	}

	if f, err := os.Create(ctx.historyFile); err == nil {
		ctx.line.WriteHistory(f)
		f.Close()
	}
}

//
// PushScope pushes a new scope for variables, with the associated dvalues
//
func (ctx *Context) PushScope(vars map[string]string, args []string) {
	scope := make(Arguments)

	for k, v := range vars {
		scope[k] = v
	}

	for i, v := range args {
		k := strconv.Itoa(i)
		scope[k] = v
	}

	if args != nil {
		scope["*"] = strings.Join(args[1:], " ") // all args
		scope["#"] = strconv.Itoa(len(args[1:])) // args[0] is the function name
	}

	ctx.scopes = append(ctx.scopes, scope)
}

//
// PopScope removes the current scope, restoring the previous one
//
func (ctx *Context) PopScope() {
	l := len(ctx.scopes)
	if l == 0 {
		panic("no scopes")
	}

	ctx.scopes = ctx.scopes[:l-1]
}

//
// GetScope returns the current scope or the global one if selected
//
func (ctx *Context) GetScope(global bool) Arguments {
	l := len(ctx.scopes)
	if l == 0 {
		return nil
	}

	if global {
		return ctx.scopes[0]
	} else {
		return ctx.scopes[l-1]
	}
}

//
// SetVar sets a variable in the current or global scope
//
func (ctx *Context) SetVar(k string, v interface{}, global bool) {
	l := len(ctx.scopes)
	if l == 0 {
		panic("no scopes")
	}

	if global {
		l = 1
	}
	ctx.scopes[l-1][k] = fmt.Sprintf("%v", v)
}

//
// GetVar return the value of the specified variable from the closest scope
//
func (ctx *Context) GetVar(k string) (string, bool) {
	for i := len(ctx.scopes) - 1; i >= 0; i-- {
		if v, ok := ctx.scopes[i][k]; ok {
			return v, true
		}
	}

	return "", false
}

//
// A basic scanner interface
//
type BasicScanner interface {
	Scan(prompt string) bool
	Text() string
	Err() error
}

//
// An implementation of basicScanner that works on a list of lines
//
type ScanLines struct {
	lines []string
}

func (s *ScanLines) Scan(prompt string) bool {
	return len(s.lines) > 0
}

func (s *ScanLines) Text() (text string) {
	if len(s.lines) == 0 {
		return
	}

	text, s.lines = s.lines[0], s.lines[1:]
	return
}

func (s *ScanLines) Err() (err error) {
	return
}

//
// An implementation of basicScanner that works with "liner"
//
type ScanLiner struct {
	line *liner.State
	text string
	err  error
}

func (s *ScanLiner) Scan(prompt string) bool {
	s.text, s.err = s.line.Prompt(prompt)
	return s.err == nil
}

func (s *ScanLiner) Text() string {
	return s.text
}

func (s *ScanLiner) Err() error {
	return s.err
}

//
// An implementation of basicScanner that works with an io.Reader (wrapped in a bufio.Scanner)
//
type ScanReader struct {
	sr *bufio.Scanner
}

func (s *ScanReader) Scan(prompt string) bool {
	return s.sr.Scan()
}

func (s *ScanReader) Text() string {
	return s.sr.Text()
}

func (s *ScanReader) Err() error {
	return s.sr.Err()
}

//
// SetScanner sets the current scanner and return the previos one
//
func (ctx *Context) SetScanner(curr BasicScanner) (prev BasicScanner) {
	prev, ctx.scanner = ctx.scanner, curr
	return
}

//
// ScanLiner sets the current scanner to a "liner" scanner
//
func (ctx *Context) ScanLiner() BasicScanner {
	return ctx.SetScanner(&ScanLiner{line: ctx.line})
}

//
// ScanBlock sets the current scanner to a block scanner
//
func (ctx *Context) ScanBlock(block []string) BasicScanner {
	return ctx.SetScanner(&ScanLines{lines: block})
}

//
// ScanReader sets the current scanner to an io.Reader scanner
//
func (ctx *Context) ScanReader(r io.Reader) BasicScanner {
	return ctx.SetScanner(&ScanReader{sr: bufio.NewScanner(r)})
}

func (ctx *Context) readOneLine(prompt string) (line string, err error) {
	if ctx.scanner.Scan(prompt) {
		line = ctx.scanner.Text()
	} else if ctx.scanner.Err() != nil {
		err = ctx.scanner.Err()
	} else {
		err = io.EOF
	}

	// fmt.Printf("readOneLine:%v %q %v\n", prompt, line, err)
	return
}

func (ctx *Context) ReadLine(prompt, cont string) (line string, err error) {
	line, err = ctx.readOneLine(prompt)
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

		l, err := ctx.readOneLine(cont)
		if err != nil {
			fmt.Fprintln(os.Stderr, "continuation", err)
			break
		}

		line += " " + strings.TrimSpace(l)
	}

	return
}

func (ctx *Context) ReadBlock(body, next, cont string) ([]string, []string, error) {
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

		line, err = ctx.ReadLine(cont, cont)
		if err != nil {
			return nil, nil, err
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			block1 = append(block1, line)
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

		line, err = ctx.ReadLine(cont, cont)
		if err != nil {
			return nil, nil, err
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			block2 = append(block2, line)
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

func (ctx *Context) ResetTerminal() {
	if ctx.line != nil {
		ctx.line.Close()
	}
}

func (ctx *Context) TerminalMode() (mode liner.ModeApplier) {
	mode, _ = liner.TerminalMode()
	return
}

func (ctx *Context) RestoreMode(m liner.ModeApplier) {
	if m != nil {
		m.ApplyMode()
	}
}