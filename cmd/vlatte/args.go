package main

import (
	"strings"

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

		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if !strings.HasPrefix(arg, "-") || name == "" || strings.HasPrefix(name, "-") {
			out.http = append(out.http, arg)
			continue
		}

		// A flag that takes a value, without =, takes the next argument, whatever it looks like.
		if takesValue[name] && !hasValue && i+1 < len(args) {
			i++
			value, hasValue = args[i], true
			arg += "=" + value
		}

		switch {
		case !hasValue && (name == "h" || name == "help"):
			send(_toSettings|_toHTTP, arg)
		case name == "c" || name == "config":
			// The gRPC server and the recipes know the config file by its long name only.
			long := "--config"
			if hasValue {
				long += "=" + value
			}

			send(_toSettings|_toHTTP, arg)
			send(_toGRPC|_toRecipe, long)
		case owners[name] != 0:
			send(owners[name], arg)
		case strings.HasPrefix(name, "grpc"):
			send(_toGRPC, arg)
		case strings.HasPrefix(name, "recipe"):
			send(_toRecipe, arg)
		default:
			send(_toHTTP, arg)
		}
	}

	return out
}
