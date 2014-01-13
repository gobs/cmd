cmd
===

A library to create shell-like command processors, slightly inspired by the Python cmd/cmd2 package

## Installation
    $ go get github.com/gobs/cmd

## Documentation
http://godoc.org/github.com/gobs/cmd

## Example

    import "github.com/gobs/cmd"
    
    // return true to stop command loop
    func Exit(line string) (stop bool) {
          fmt.Println("goodbye!")
          return true
    }
      
    // change the prompt
    func (cmd *Cmd) SetPrompt(line string) (stop bool) {
    cmd.Prompt = line
    return
    }
    
    // initialize Cmd structure
    commander := &cmd.Cmd{Prompt: "> ",}
    commander.Init()
    
    // add inline method
    commander.Add(cmd.Command{
          "ls",
          `list stuff`,
          func(line string) (stop bool) {
              fmt.Println("listing stuff")
              return
	  }})

    // add another command
    commander.Add(cmd.Command{
          Name: "prompt",
          Help: `Set prompt`,
          Call: commander.SetPrompt
          })
    
    // and one more
    commander.Add(cmd.Command{
          "exit",
          `terminate example`,
          Exit
	  })

    // start command loop
    commander.CmdLoop()

