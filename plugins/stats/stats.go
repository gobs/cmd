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

func parseFloat(v string) (float64, error) {
	return strconv.ParseFloat(v, 64)
}

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
                stats {count|min|max|mean|median|sum|variance|std|pN} value...
                `,
		func(line string) (stop bool) {
			var res float64
			var err error

			parts := args.GetArgs(line) // [ type, value, ... ]
			if len(parts) == 0 {
				fmt.Println("usage: stats {count|min|max|mean|median|sum|variance|std|pN} value...")
				return
			}

			if len(parts) == 1 {
				res = 0.0
			} else {
				cmd, parts := parts[0], parts[1:]
				sample := false
				population := false
				geometric := false
				harmonic := false
				nearestRank := false

				if len(parts) > 0 {
					switch parts[1] {
					case "-g", "--geometric":
						geometric = true
						parts = parts[1:]

					case "-h", "--harmonic":
						harmonic = true
						parts = parts[1:]

					case "-s", "--sample":
						sample = true
						parts = parts[1:]

					case "-p", "--population":
						population = true
						parts = parts[1:]

					case "-n", "--nearest-rank":
						nearestRank = true
						parts = parts[1:]
					}
				}

				data := stats.LoadRawData(parts)
				pc := 0.0

				if strings.HasPrefix(cmd, "p") {
					pc, err = parseFloat(cmd[1:])
					if err != nil {
						fmt.Println("invalid percentile command:", cmd)
						return
					}

					cmd = "p"
				}

				switch cmd {
				case "count":
					res = float64(len(data))
				case "min":
					res, err = data.Min()
				case "max":
					res, err = data.Max()
				case "mean":
					if geometric {
						res, err = data.GeometricMean()
					} else if harmonic {
						res, err = data.HarmonicMean()
					} else {
						res, err = data.Mean()
					}
				case "median":
					res, err = data.Median()
				//case "mode":
				//	res, err = data.Mode()
				case "sum":
					res, err = data.Sum()
				case "variance":
					if sample {
						res, err = data.SampleVariance()
					} else if population {
						res, err = data.PopulationVariance()
					} else {
						res, err = data.Variance()
					}
				case "std":
					if sample {
						res, err = data.StandardDeviationSample()
					} else if population {
						res, err = data.StandardDeviationPopulation()
					} else {
						res, err = data.StandardDeviation()
					}
				case "p":
					if nearestRank {
						res, err = data.PercentileNearestRank(pc)
					} else {
						res, err = data.Percentile(pc)
					}
				default:
					fmt.Println("usage: stats {count|min|max|mean|median|sum|variance|std|pN} value...")
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
