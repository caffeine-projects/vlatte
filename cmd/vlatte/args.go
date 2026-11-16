package main

import (
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// commandArgs are the command's arguments, split by the settings that read them, each flag with its value.
type commandArgs struct {
	settings []string // the command's own settings, such as --admin-addr
	http     []string
	grpc     []string
	recipe   []string
}

// flagOwner is the set of settings that read a flag, one bit for each field of commandArgs.
type flagOwner uint8

const (
	_toSettings flagOwner = 1 << iota
	_toHTTP
	_toGRPC
	_toRecipe
)

// _flagTable returns the settings that read each flag of the command, by name and shorthand, and whether it takes a
// value: the tables splitArgs and joinValues read, built once.
var _flagTable = sync.OnceValues(func() (map[string]flagOwner, map[string]bool) {
	owners := make(map[string]flagOwner)
	takesValue := make(map[string]bool)

	add := func(f config.Flag, to flagOwner) {
		for _, name := range []string{f.Name, f.Shorthand} {
			if name != "" {
				owners[name] |= to
				takesValue[name] = !f.Bool
			}
		}
	}

	for _, f := range mockhttp.ConfigFlags() {
		add(f, _toHTTP)
	}

	for _, f := range mockgrpc.ConfigFlags() {
		add(f, _toGRPC)
	}

	for _, f := range _recipeSchema().Flags() {
		add(f, _toRecipe)
	}

	// The command reads some settings itself, such as admin.addr or grpc.admin: the server of their section accepts
	// them too, through hidden mirrors.
	for _, f := range settingsSchema().Flags() {
		to := _toSettings | _toHTTP
		if strings.HasPrefix(f.Name, "grpc") {
			to = _toSettings | _toGRPC
		}

		add(f, to)
	}

	return owners, takesValue
})

// joinValues joins each flag that takes a value, before the first argument that isn't a flag, with its separate
// value, as -flag=value: cobra picks the command from the first such argument, and must not take a flag's value
// for one. The arguments from the first that isn't a flag, or from --, are left as they are. It never returns nil:
// cobra reads os.Args for nil.
func joinValues(args []string) []string {
	_, takesValue := _flagTable()
	out := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		f, ok := readFlag(args, i, takesValue)
		if !ok {
			return append(out, args[i:]...)
		}

		out = append(out, f.arg)
		i = f.last
	}

	return out
}

// flagArg is a flag of the command's arguments, read as Go's flag package reads it.
type flagArg struct {
	arg      string // the flag as written, or as -flag=value once it took its value from the next argument
	name     string
	value    string
	hasValue bool
	last     int // the index of the last argument it read: its own, or that of its value
}

// readFlag reads args[i] as a flag, and reports whether it is one. A flag that takes a value, written without =,
// takes the next argument as its value, whatever it looks like.
func readFlag(args []string, i int, takesValue map[string]bool) (flagArg, bool) {
	f := flagArg{arg: args[i], last: i}

	f.name, f.value, f.hasValue = strings.Cut(strings.TrimPrefix(strings.TrimPrefix(f.arg, "-"), "-"), "=")
	if !strings.HasPrefix(f.arg, "-") || f.name == "" || strings.HasPrefix(f.name, "-") {
		return flagArg{}, false
	}

	if takesValue[f.name] && !f.hasValue && i+1 < len(args) {
		f.last = i + 1
		f.value, f.hasValue = args[f.last], true
		f.arg += "=" + f.value
	}

	return f, true
}

// splitArgs splits the command's arguments between the settings that read them, as Go's flag package reads them,
// before any settings load, so that each flag and its value go together.
//
// A flag goes to its owners: the command's own settings, the HTTP server's settings, the gRPC server's and the
// recipes'. A flag of the command's own settings also goes to the gRPC server when its name starts with grpc, and to
// the HTTP server otherwise, where hidden mirrors accept it. An unknown flag goes to the gRPC server when its name
// starts with grpc, to the recipes when it starts with recipe, and to the HTTP server otherwise, which reports it.
//
// A flag that takes a value, without =, takes the next argument as its value, whatever it looks like, -- included:
// each owner gets it as -flag=value, in the spelling the flag had. The config file, --config or -c, goes to all of
// them. The flags end at --: it and what follows go to the HTTP server, which rejects them as unexpected arguments.
func splitArgs(args []string) commandArgs {
	owners, takesValue := _flagTable()

	var out commandArgs

	dsts := map[flagOwner]*[]string{_toSettings: &out.settings, _toHTTP: &out.http, _toGRPC: &out.grpc, _toRecipe: &out.recipe}
	send := func(to flagOwner, arg string) {
		for owner, dst := range dsts {
			if to&owner != 0 {
				*dst = append(*dst, arg)
			}
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			out.http = append(out.http, args[i:]...)
			break
		}

		f, ok := readFlag(args, i, takesValue)
		if !ok {
			out.http = append(out.http, arg)
			continue
		}

		i = f.last

		switch {
		case f.name == "h" || f.name == "help":
			send(_toSettings|_toHTTP, f.arg)
		case f.name == "c" || f.name == "config":
			// The gRPC server and the recipes know the config file by its long name only.
			long := "--config"
			if f.hasValue {
				long += "=" + f.value
			}

			send(_toSettings|_toHTTP, f.arg)
			send(_toGRPC|_toRecipe, long)
		case owners[f.name] != 0:
			send(owners[f.name], f.arg)
		case strings.HasPrefix(f.name, "grpc"):
			send(_toGRPC, f.arg)
		case strings.HasPrefix(f.name, "recipe"):
			send(_toRecipe, f.arg)
		default:
			send(_toHTTP, f.arg)
		}
	}

	return out
}
