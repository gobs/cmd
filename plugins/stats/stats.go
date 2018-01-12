// Package stats add some statistics-related commands to the command loop.
//
// The new commands are in the form:
//
// stats {type} values...
//
package stats

import (
	"fmt"
	"math"
	"sort"
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

// sortedCopy returns a sorted copy of float64s
func sortedCopy(input stats.Float64Data) (sorted stats.Float64Data) {
	sorted = make(stats.Float64Data, input.Len())
	copy(sorted, input)
	sort.Float64s(sorted)
	return
}

// Percentile finds the relative standing in a slice of floats
// (note: the "Percentile" method in "stats" is incorrect)

func Percentile(input stats.Float64Data, percent float64) (percentile float64, err error) {

	if input.Len() == 0 {
		return math.NaN(), stats.EmptyInput
	}

	if percent < 0 || percent > 100 {
		return math.NaN(), stats.BoundsErr
	}

	// Start by sorting a copy of the slice
	sorted := sortedCopy(input)

	// Edge cases
	if percent == 0.0 { // The percentile argument of 0 will return the minimum value in the dataset.
		return sorted[0], nil
	}
	if percent == 50.0 { // The percentile argument of 50 returns the median value.
		return sorted.Median()
	}
	if percent == 100.0 { // The percentile argument of 100 returns the maximum value from the dataset.
		return sorted[len(sorted)-1], nil
	}

	// Find the rank. Rank is the position of an element in the dataset.
	rank := ((percent / 100) * float64(len(sorted)-1))

	ri := int(rank)
	rf := rank - float64(ri)

	percentile = sorted[ri] + rf*(sorted[ri+1]-sorted[ri])
	return
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
						res, err = Percentile(data, pc)
					}
				default:
					fmt.Println("usage: stats {count|min|max|mean|median|sum|variance|std|pN} value...")
					return
				}
			}

			if err != nil {
				commander.SetVar("error", err)
				commander.SetVar("result", "0")
				fmt.Println(err)
			} else {
				sres := floatString(res)
				if !commander.SilentResult() {
					fmt.Println(sres)
				}

				commander.SetVar("error", "")
				commander.SetVar("result", sres)
			}

			return
		},
		nil})

	return nil
}
