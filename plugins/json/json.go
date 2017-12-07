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
	"github.com/gobs/jsonpath"
	"github.com/gobs/simplejson"
)

var reFieldValue = regexp.MustCompile(`(\w[\d\w-]*)(=(.*))?`) // field-name=value

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
		return strings.Trim(v, `"`), nil

	case strings.HasPrefix(v, `'`):
		return strings.Trim(v, `'`), nil

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

func unquote(s string) string {
	if res, err := strconv.Unquote(strings.TrimSpace(s)); err == nil {
		return res
	}

	return s
}

// Function PrintJson prints the specified object formatted as a JSON object
func PrintJson(v interface{}) {
	fmt.Println(simplejson.MustDumpString(v, simplejson.Indent("  ")))
}

// Function StringJson return the specified object as a JSON string
func StringJson(v interface{}, unq bool) (ret string) {
	ret = simplejson.MustDumpString(v)
	if unq {
		return unquote(ret)
	}

	return
}

// Function Init adds json-related commands to the specified "commander"
func Init(commander *cmd.Cmd) {
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
					err = fmt.Errorf("error parsing object %q", line)
					commander.Vars["error"] = err.Error()
					fmt.Println(err)
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
							fmt.Println(err)
							commander.Vars["error"] = err.Error()
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
							fmt.Println(err)
							commander.Vars["error"] = err.Error()
							return
						}
					} else {
						fmt.Println("invalid name=value pair:", f)
						commander.Vars["error"] = "invalid name=value pair"
						return
					}
				}

				res = mres
			}

			if commander.GetBoolVar("print", true) {
				PrintJson(res)
			}

			commander.Vars["error"] = ""
			commander.Vars["json"] = StringJson(res, true)
			return
		},
		nil})

	commander.Add(cmd.Command{
		"jsonpath",
		`jsonpath [-e] [-c] path {json}`,
		func(line string) (stop bool) {
			var joptions jsonpath.ProcessOptions

			options, line := args.GetOptions(line)
			for _, o := range options {
				if o == "-e" || o == "--enhanced" {
					joptions |= jsonpath.Enhanced
				} else if o == "-c" || o == "--collapse" {
					joptions |= jsonpath.Collapse
				} else {
					line = "" // to force an error
					break
				}
			}

			parts := args.GetArgsN(line, 2)
			if len(parts) != 2 {
				fmt.Println("use: jsonpath [-e|--enhanced] path {json}")
				commander.Vars["error"] = "invalid-usage"
				return
			}

			path := parts[0]
			if !strings.HasPrefix(path, "$.") {
				path = "$." + path
			}

			jbody, err := simplejson.LoadString(parts[1])
			if err != nil {
				fmt.Println("json:", err)
				commander.Vars["error"] = err.Error()
				return
			}

			jp := jsonpath.NewProcessor()
			if !jp.Parse(path) {
				commander.Vars["error"] = fmt.Sprintf("failed to parse %q", path)
				return // syntax error
			}

			res := jp.Process(jbody, joptions)
			if commander.GetBoolVar("print", true) {
				PrintJson(res)
			}
			commander.Vars["error"] = ""
			commander.Vars["json"] = StringJson(res, true)
			return
		},
		nil})

	commander.Add(cmd.Command{
		"format",
		`format object`,
		func(line string) (stop bool) {
			jbody, err := simplejson.LoadString(line)
			if err != nil {
				fmt.Println("json:", err)
				commander.Vars["error"] = err.Error()
				return
			}

			PrintJson(jbody.Data())
			return
		},
		nil})
}
