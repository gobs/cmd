package main

import (
	"github.com/gobs/cmd"

	"fmt"
)

func Exit(line string) (stop bool) {
	fmt.Println("goodbye!")
	return true
}

func main() {
	commander := &cmd.Cmd{EnableShell: true}
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
