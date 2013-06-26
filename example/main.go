package main

import (
    "github.com/gobs/cmd"

    "fmt"
    )

func main() {
    commander := &cmd.Cmd{}
    commander.Init()

    commander.Commands["ls"] = func(line string) (stop bool) {
        fmt.Println("listing stuff")
        return
    }

    commander.Commands["exit"] = func(line string) (stop bool) {
        fmt.Println("goodbye!")
        return true
    }

    commander.CmdLoop()
}
