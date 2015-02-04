package main

import (
	"github.com/gobs/args"
	"github.com/gobs/cmd"

	"fmt"
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

func Exit(line string) (stop bool) {
	fmt.Println("goodbye!")
	return true
}

func main() {
	commander := &cmd.Cmd{HistoryFile: ".rlhistory", Complete: CompletionFunction, EnableShell: true}
	commander.Init()

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
			fmt.Println("sleeping...")
			time.Sleep(10 * time.Second)
			return
		},
		nil,
	})

	commander.Add(cmd.Command{
		Name: ">",
		Help: `Set prompt`,
		Call: func(line string) (stop bool) {
			commander.Prompt = line
			return
		}})

	commander.Add(cmd.Command{
		Name: "args",
		Help: "parse args",
		Call: func(line string) (stop bool) {
			fmt.Printf("%q\n", args.GetArgs(line))
			return
		}})

	commander.Add(cmd.Command{
		"exit",
		`terminate example`,
		Exit,
		nil})

	commander.CmdLoop()
}
