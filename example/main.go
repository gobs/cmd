package main

import (
	"github.com/gobs/cmd"

	"fmt"
	"strings"
)

var (
	words = []string{"one", "two", "three", "four"}
)

func CompletionFunction(text string) []string {
	matches := make([]string, 0, len(words))

	for _, w := range words {
		if strings.HasPrefix(w, text) {
			matches = append(matches, w)
		}
	}

	return matches
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
		}})

	commander.Add(cmd.Command{
		Name: ">",
		Help: `Set prompt`,
		Call: func(line string) (stop bool) {
			commander.Prompt = line
			return
		}})

	commander.Add(cmd.Command{
		"exit",
		`terminate example`,
		Exit})

	commander.CmdLoop()
}
