// Package json add some json-related commands to the command loop.
//
// The new commands are:
//
//	json : creates a json object out of key/value pairs or lists
//	jsonpath : parses a json object and extract specified fields
//	format : pretty-print specified json object
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
			return nil, fmt.Errorf("error parsing |%v|", v)
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

type map_type = map[string]interface{}
type array_type = []interface{}

func merge_maps(dst, src map_type) map_type {
	for k, vs := range src {
		if vd, ok := dst[k]; ok {
			if ms, ok := vs.(map_type); ok {
				if md, ok := vd.(map_type); ok {
					merge_maps(md, ms)
					continue
				}
			}
		}
		dst[k] = vs
	}

	return dst
}

func merge_array(dst array_type, src interface{}) array_type {
	if a, ok := src.(array_type); ok {
		return append(dst, a...)
	} else {
		return append(dst, src)
	}
}

// PluginInit initialize this plugin
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
                json [value1, value2...]
                json -a|--array value1 value2 value3`,
		func(line string) (stop bool) {
			var res interface{}
			var ares []interface{}

			if strings.HasPrefix(line, "-a ") {
				line = strings.TrimSpace(line[3:])
				ares = []interface{}{}
			} else if strings.HasPrefix(line, "--array ") {
				line = strings.TrimSpace(line[8:])
				ares = []interface{}{}
			}

			for len(line) > 0 {
				jbody, rest, err := simplejson.LoadPartialString(line)
				if err == nil {
					switch v := res.(type) {
					case nil: // first time
						res = jbody.Data()

					case map_type:
						src, err := jbody.Map()
						if err != nil {
							setError(fmt.Errorf("merge source should be a map"))
							return
						}
						res = merge_maps(v, src)

					case array_type:
						res = merge_array(v, jbody.Data())
					}
				} else {
					args := args.GetArgsN(line, 2, args.InfieldBrackets())

					matches := reFieldValue.FindStringSubmatch(args[0])
					if len(matches) > 0 { // [field=value field =value value]
						name, svalue := matches[1], matches[3]
						value, err := parseValue(svalue)
						if err != nil {
							setError(err)
							return
						}

						mval := map[string]interface{}{name: value}

						switch v := res.(type) {
						case nil: // first time
							res = mval

						case map_type:
							res = merge_maps(v, mval)

						case array_type:
							res = merge_array(v, mval)
						}
					} else {
						setError(fmt.Errorf("invalid name=value pair: %v", args[0]))
						return
					}

					if len(args) == 2 {
						rest = args[1]
					} else {
						break
					}
				}

				line = strings.TrimSpace(rest)
				if ares != nil {
					ares = append(ares, res)
					res = nil
				}
			}

			if ares == nil {
				setJson(res)
			} else {
				setJson(ares)
			}
			return
		},
		nil})

	commander.Add(cmd.Command{
		"jsonpath",
		`jsonpath [-v] [-e] [-c] path {json}`,
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
