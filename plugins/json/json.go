// Package json add some json-related commands to the command loop.
//
// The new commands are:
//
//   json : creates a json object out of key/value pairs or lists
//   jsonpath : parses a json object and extract specified fields
//   format : pretty-print specified json object
//
package json

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gobs/args"
	"github.com/gobs/cmd"
	"github.com/gobs/cmd/internal"
	"github.com/gobs/jsonpath"
	"github.com/gobs/simplejson"
)

type jsonPlugin struct {
	cmd.Plugin
}

var (
	Plugin = &jsonPlugin{}

	reFieldValue = regexp.MustCompile(`(\w[\d\w-]*)(=(.*))?`) // field-name=value
)

func unquote(s string, q byte) (string, error) {
	l := len(s)
	if l == 1 {
		return s, fmt.Errorf("tooshort")
	}

	if s[l-1] == q {
		return s[1 : l-1], nil
	}

	return s, fmt.Errorf("unbalanced")
}

func parseValue(v string) (interface{}, error) {
	switch {
	case strings.HasPrefix(v, "{") || strings.HasPrefix(v, "["):
		j, err := simplejson.LoadString(v)
		if err != nil {
			return nil, fmt.Errorf("error parsing %q", v)
		} else {
			return j.Data(), nil
		}

	case strings.HasPrefix(v, `"`):
		return unquote(v, '"')

	case strings.HasPrefix(v, `'`):
		return unquote(v, '\'')

	case strings.HasPrefix(v, "`"):
		return unquote(v, '`')

	case v == "":
		return v, nil

	case v == "true":
		return true, nil

	case v == "false":
		return false, nil

	case v == "null":
		return nil, nil

	default:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, nil
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, nil
		}

		return v, nil
	}
}

// Function PrintJson prints the specified object formatted as a JSON object
func PrintJson(v interface{}) {
	fmt.Println(simplejson.MustDumpString(v, simplejson.Indent("  ")))
}

// Function StringJson return the specified object as a JSON string
func StringJson(v interface{}, unq bool) (ret string) {
	ret = simplejson.MustDumpString(v)
	if unq {
		ret, _ = unquote(strings.TrimSpace(ret), '"')
	}

	return
}

//
// PluginInit initialize this plugin
//
func (p *jsonPlugin) PluginInit(commander *cmd.Cmd, _ *internal.Context) error {

	setError := func(err interface{}) {
		fmt.Println(err)
		commander.SetVar("error", err)
	}

	setJson := func(v interface{}) {
		commander.SetVar("json", StringJson(v, true))
		commander.SetVar("error", "")

		if !commander.SilentResult() {
			PrintJson(v)
		}
	}

	commander.Add(cmd.Command{"json",
		`
                json field1=value1 field2=value2...       // json object
                json {"name1":"value1", "name2":"value2"}
                json [value1 value2...]                   // json array
                json [value1, value2...]`,
		func(line string) (stop bool) {
			var res interface{}

			if strings.HasPrefix(line, "{") { // assume is already a JSON object

				if jbody, err := simplejson.LoadString(line); err != nil {
					setError(fmt.Errorf("error parsing object %q", line))
					return
				} else {
					res = jbody.Data()
				}
			} else if strings.HasPrefix(line, "[") { // could be a JSON array

				if jbody, err := simplejson.LoadString(line); err == nil {
					res = jbody.Data()
				} else { // try a sequence of values (that need to be parsed)
					line = strings.TrimPrefix(line, "[")
					line = strings.TrimSuffix(line, "]")
					line = strings.TrimSpace(line)

					var ares []interface{}

					for _, f := range args.GetArgs(line) {
						v, err := parseValue(f)
						if err != nil {
							setError(err)
							return
						}

						ares = append(ares, v)
					}

					res = ares
				}
			} else { // a sequence of name=value pairs
				var err error
				mres := map[string]interface{}{}

				for _, f := range args.GetArgs(line, args.InfieldBrackets()) {
					matches := reFieldValue.FindStringSubmatch(f)
					if len(matches) > 0 { // [field=value field =value value]
						name, value := matches[1], matches[3]
						mres[name], err = parseValue(value)

						if err != nil {
							setError(err)
							return
						}
					} else {
						setError(fmt.Errorf("invalid name=value pair: %v", f))
						return
					}
				}

				res = mres
			}

			setJson(res)
			return
		},
		nil})

	commander.Add(cmd.Command{
		"jsonpath",
		`jsonpath [-e] [-c] path {json}`,
		func(line string) (stop bool) {
			var joptions jsonpath.ProcessOptions
                        var verbose bool

			options, line := args.GetOptions(line)
			for _, o := range options {
				if o == "-e" || o == "--enhanced" {
					joptions |= jsonpath.Enhanced
				} else if o == "-c" || o == "--collapse" {
					joptions |= jsonpath.Collapse
				} else if o == "-v" || o == "--verbose" {
					verbose = true
				} else {
					line = "" // to force an error
					break
				}
			}

			parts := args.GetArgsN(line, 2)
			if len(parts) != 2 {
				setError("invalid-usage")
				return
			}

			path := parts[0]
			if !(strings.HasPrefix(path, "$.") || strings.HasPrefix(path, "$[")) {
				path = "$." + path
			}

			jbody, err := simplejson.LoadString(parts[1])
			if err != nil {
				setError(err)
				return
			}

			jp := jsonpath.NewProcessor()
			if !jp.Parse(path) {
				setError(fmt.Errorf("failed to parse %q", path))
				return // syntax error
			}

                        if verbose {
                            fmt.Println("jsonpath", path)
                            for _, n := range jp.Nodes {
			        fmt.Println(" ", n)
		            }
                        }

			res := jp.Process(jbody, joptions)
			setJson(res)
			return
		},
		nil})

	commander.Add(cmd.Command{
		"format",
		`format object`,
		func(line string) (stop bool) {
			jbody, err := simplejson.LoadString(line)
			if err != nil {
				fmt.Println("format:", err)
				fmt.Println("input:", line)
				return
			}

			PrintJson(jbody.Data())
			return
		},
		nil})

	return nil
}
