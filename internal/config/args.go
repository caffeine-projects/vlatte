package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type argsSource struct {
	args []string
}

// Args reads settings from command-line arguments, such as --log-level=debug, -a :8080 or --cors.
// Flag names derive from keys, with "-" for "." and "_". A repeated list flag adds to the list.
// It never reads os.Args: the caller passes the arguments, such as os.Args[1:].
func Args(args []string) Source {
	return &argsSource{args: args}
}

func (s *argsSource) load(c *catalog, _ lookup) ([]entry, []Problem, error) {
	if len(s.args) == 0 {
		return nil, nil, nil
	}

	fs := flag.NewFlagSet("args", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var entries []entry
	lists := make(map[string]int) // index of each list's entry, so repeated flags add to it

	for name, f := range c.byFlag {
		record := func(raw string) error {
			if i, ok := lists[f.key]; ok {
				prev, _ := entries[i].raw.(string) // list entries always hold the raw string
				entries[i].raw = prev + "," + raw
				return nil
			}

			if f.kind == List {
				lists[f.key] = len(entries)
			}

			entries = append(entries, entry{key: f.key, raw: raw, origin: "flag " + dashed(name)})

			return nil
		}

		if f.kind == Bool {
			fs.BoolFunc(name, f.usage, record)
		} else {
			fs.Func(name, f.usage, record)
		}
	}

	err := fs.Parse(s.args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return nil, nil, flag.ErrHelp
	case err != nil:
		return entries, []Problem{flagProblem(c, err)}, nil
	}

	rest := fs.Args()
	first := len(s.args) - len(rest)

	var problems []Problem
	for i, arg := range rest {
		// A URL meant for a bool flag, as in "--proxy http://user:password@host", ends up here.
		shown := display(arg)
		p := Problem{
			Code:    CodeUnexpectedArgument,
			Origin:  "args",
			Value:   shown,
			Message: fmt.Sprintf("unexpected argument %q: only flags are accepted", shown),
		}

		// "--cors false" sets cors and leaves "false" behind.
		if _, err := strconv.ParseBool(arg); err == nil && i == 0 && first > 0 {
			prev := s.args[first-1]
			if f := c.byFlag[strings.TrimLeft(prev, "-")]; f != nil && f.kind == Bool && prev != "--" {
				p.Hint = fmt.Sprintf("a bool flag takes its value after =, as in %s=%s", prev, arg)
			}
		}

		problems = append(problems, p)
	}

	return entries, problems, nil
}

// flagProblem describes an error of the flag package.
func flagProblem(c *catalog, err error) Problem {
	msg := err.Error()

	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		p := Problem{Code: CodeUnknownKey, Origin: "flag " + dashed(name), Message: dashed(name) + " is not a known flag"}

		names := make([]string, 0, len(c.fields))
		for _, f := range c.fields {
			names = append(names, f.flag)
		}

		if near := suggest(name, names); near != "" {
			p.Hint = "did you mean " + dashed(near) + "?"
		}

		return p
	}

	if name, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		p := Problem{
			Code:    CodeMissingValue,
			Origin:  "flag " + dashed(name),
			Message: dashed(name) + " needs a value",
			Hint:    "write " + dashed(name) + "=<value>",
		}

		if f := c.byFlag[name]; f != nil {
			p.Key = f.key
		}

		return p
	}

	return Problem{Code: CodeParseError, Origin: "args", Message: msg}
}

// dashed writes a flag name the way it is typed: -a for one letter, --name otherwise.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}

	return "--" + name
}
