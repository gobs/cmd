package main

import (
	"github.com/gobs/args"
	"github.com/gobs/cmd"
	//"github.com/gobs/cmd/plugins/json"
	"github.com/gobs/cmd/plugins/controlflow"

	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	words = []string{"one", "two", "three", "four"}
)

func CompletionFunction(text, line string) (matches []string) {
	// for the "ls" command we let readline show real file names
	if strings.HasPrefix(line, "ls ") {
		return
	}

	// for all other commands, we pick from our list of completion words
	for _, w := range words {
		if strings.HasPrefix(w, text) {
			matches = append(matches, w)
		}
	}

	return
}

func main() {
	commander := &cmd.Cmd{HistoryFile: ".rlhistory", Complete: CompletionFunction, EnableShell: true}
	commander.Init(controlflow.Plugin)

	/*
		commander.Vars = map[string]string{
			"user": "Bob",
			"cwd":  "/right/here",
			"ret":  "42",
		}
	*/

	commander.Add(cmd.Command{
		"ls",
		`list stuff`,
		func(line string) (stop bool) {
			fmt.Println("listing stuff")
			return
		},
		nil})

	commander.Add(cmd.Command{
		"sleep",
		`sleep for a while`,
		func(line string) (stop bool) {
			s := time.Second

			if t, err := strconv.Atoi(line); err == nil {
				s *= time.Duration(t)
			}

			fmt.Println("sleeping...")
			time.Sleep(s)
			return
		},
		nil,
	})

	commander.Add(cmd.Command{
		Name: ">",
		Help: `Set prompt`,
		Call: func(line string) (stop bool) {
			// commander.Prompt = line  // set prompt
			commander.SetPrompt(line, 20) // set prompt with max length of 20
			return
		}})

	commander.Add(cmd.Command{
		Name: "timing",
		Help: `Enable timing`,
		Call: func(line string) (stop bool) {
			line = strings.ToLower(line)
			commander.Timing = line == "true" || line == "yes" || line == "1" || line == "on"
			return
		}})

	commander.Add(cmd.Command{
		Name: "args",
		Help: "parse args",
		Call: func(line string) (stop bool) {
			fmt.Printf("%q\n", args.GetArgs(line))
			return
		}})

	//json.Init(commander)

	if len(os.Args) > 1 {
		cmd := strings.Join(os.Args[1:], " ")
		commander.OneCmd(cmd)
	}

	commander.CmdLoop()

}
