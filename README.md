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

## Available commands

The command processor predefines a few useful commands, including function definitions and conditionals.

Use the `help` command to see the list of available commands.

Function definition is similart to bash functions:

    function test {
        echo Function name: $0
        echo Number of arguments: $#
        echo First argument: $1
        echo All arguments: $*
    }

but you can also define a very short function (one-liner):

    function oneliner echo "very short function"

Variables can be set/listed using the `var` command:

    var catch 22

    var catch
        catch: 22

    echo $catch
        22

To unset/remove a variable use:

    var -r catch
    var -rm catch
    var --remove catch

note that currently only "string" values are supported (i.e. `var x 1` is the same as `var x "1"1)

Conditional flow with `if` and `else` commands:

    if (condition) {
        # true path
    } else {
        # false path
    }

The `else` block is optional:

    if (condition) {
        # only the truth
    }

And the short test:

    if (condition) echo "yes!"

## Conditions:

The simplest condition is the "non empty argument":

    if true echo "yes, it's true"

But if you are using a variable you need to quote it:

    if "$nonempty" echo "nonempty is not empty"

All other conditionals are in the form: `(cond arguments...)`:

    (z $var)        # $var is empty

    (n $var)        # $var is not empty

    (t $var)        # $var is true (true, 1, not-empty var)

    (f $var)        # $var is false (false, 0, empty empty)
 
    (eq $var val)   # $var == val

    (ne $var val)   # $var != val

    (gt $var val)   # $var > val

    (gte $var val)  # $var >= val

    (lt $var val)   # $var < val

    (lte $var val)  # $var <= val

    (startswith $var val) # $var starts with val
    (endswith $var val)   # $var ends with val
    (contains $var val)   # $var contains val

Conditions can also be negated with the "!" operator:

    if !true echo "not true"
    if !(contains $var val) echo val not in $var

As for variables, for now only string comparisons are supported.
