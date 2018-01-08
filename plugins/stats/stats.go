// Package stats add some statistics-related commands to the command loop.
//
// The new commands are in the form:
//
// stats {type} values...
//
package stats

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gobs/args"
	"github.com/gobs/cmd"
	"github.com/gobs/cmd/internal"
	"github.com/montanaflynn/stats"
)

type statsPlugin struct {
	cmd.Plugin
}

var (
	Plugin = &statsPlugin{}
)

func floatString(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	return strings.TrimSuffix(s, ".000")
}

//
// PluginInit initialize this plugin
//
func (p *statsPlugin) PluginInit(commander *cmd.Cmd, _ *internal.Context) error {

	commander.Add(cmd.Command{"stats",
		`
                stats {min|max|mean|median|sum|variance|std|pN} value...
                `,
		func(line string) (stop bool) {
			var res float64
			var err error

			parts := args.GetArgs(line) // [ type, value, ... ]
			if len(parts) == 0 {
				fmt.Println("usage: stats {min|max|mean|median|sum|variance|std|pN} value...")
				return
			}

			if len(parts) == 1 {
				res = 0.0
			} else {
				cmd := parts[0]
				data := stats.LoadRawData(parts[1:])

				switch cmd {
				case "min":
					res, err = data.Min()
				case "max":
					res, err = data.Max()
				case "mean":
					res, err = data.Mean()
				case "median":
					res, err = data.Median()
				//case "mode":
				//	res, err = data.Mode()
				case "sum":
					res, err = data.Sum()
				case "variance":
					res, err = data.Variance()
				case "std":
					res, err = data.StandardDeviation()
				default:
					fmt.Println("usage: stats {min|max|mean|median|sum|variance|std|pN} value...")
					return
				}
			}

			if err != nil {
				commander.SetVar("error", err, true)
				commander.SetVar("result", "0", true)
				fmt.Println(err)
			} else {
				sres := floatString(res)
				if !commander.Silent() {
					fmt.Println(sres)
				}

				commander.SetVar("error", "", true)
				commander.SetVar("result", sres, true)
			}

			return
		},
		nil})

	return nil
}
