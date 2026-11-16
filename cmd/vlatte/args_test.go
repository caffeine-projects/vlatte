package main

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"flag"
	"io"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/recipe/http/githubauth"
)

func TestSplitArgs_GivesEachServerItsFlags(t *testing.T) {
	// The flags of gRPC settings go to the gRPC server, with their values, the config file to each, and the rest to
	// the HTTP server: each server reports a flag it doesn't know.
	got := splitArgs([]string{
		"--addr", ":8080",
		"--grpc",
		"--grpc-addr", ":50051",
		"--grpc-mock-files=mocks/*.yaml",
		"-c", "settings.yaml",
		"--grpc-reflection=false",
		"--grpc-admin-dir", "admin",
		"--debug",
	})

	require.Equal(t, commandArgs{
		settings: []string{"-c=settings.yaml", "--grpc-admin-dir=admin"},
		http:     []string{"--addr=:8080", "-c=settings.yaml", "--debug"},
		grpc: []string{
			"--grpc", "--grpc-addr=:50051", "--grpc-mock-files=mocks/*.yaml", "--config=settings.yaml",
			"--grpc-reflection=false", "--grpc-admin-dir=admin",
		},
		recipe: []string{"--config=settings.yaml"},
	}, got)
}

func TestSplitArgs_FlagsEndAtTheTerminator(t *testing.T) {
	// The HTTP server's parser stops at -- and rejects what follows: no gRPC flag after it configures the gRPC server.
	got := splitArgs([]string{
		"--addr", ":8080", "--grpc-addr", "--", "--", "--grpc-addr=:50051", "--grpc-descriptors=api.binpb", "-c", "x",
	})

	require.Equal(t, commandArgs{
		http: []string{"--addr=:8080", "--", "--grpc-addr=:50051", "--grpc-descriptors=api.binpb", "-c", "x"},
		grpc: []string{"--grpc-addr=--"},
	}, got, "a flag's value can be --")
}

func TestSplitArgs_GivesTheRecipesTheirFlags(t *testing.T) {
	// Every --recipe* flag goes to the recipes, with its value, even --recipe, which names no setting: the recipe
	// settings report it, with the recipe flags as suggestions.
	got := splitArgs([]string{
		"--addr", ":8080", "--recipe", "--recipe-oidc", "--recipe-oidc-client-id", "app", "--recipe-github-auth-token=t",
		"-c", "s.yaml", "--grpc",
	})

	require.Equal(t, commandArgs{
		settings: []string{"-c=s.yaml"},
		http:     []string{"--addr=:8080", "-c=s.yaml"},
		grpc:     []string{"--config=s.yaml", "--grpc"},
		recipe: []string{
			"--recipe", "--recipe-oidc", "--recipe-oidc-client-id=app", "--recipe-github-auth-token=t", "--config=s.yaml",
		},
	}, got)
}

func TestSplitArgs_KeepsAValueWithItsFlag(t *testing.T) {
	for _, tt := range []struct {
		name     string
		separate []string
		joined   []string
		want     commandArgs
	}{
		{
			name:     "an http value that looks like a recipe flag",
			separate: []string{"--name", "--recipe-oidc"},
			joined:   []string{"--name=--recipe-oidc"},
			want:     commandArgs{http: []string{"--name=--recipe-oidc"}},
		},
		{
			name:     "an http value that looks like a grpc flag",
			separate: []string{"--name", "--grpc"},
			joined:   []string{"--name=--grpc"},
			want:     commandArgs{http: []string{"--name=--grpc"}},
		},
		{
			name:     "a recipe value that looks like a setting",
			separate: []string{"--recipe-github-auth-user-name", "--config=missing.yaml"},
			joined:   []string{"--recipe-github-auth-user-name=--config=missing.yaml"},
			want:     commandArgs{recipe: []string{"--recipe-github-auth-user-name=--config=missing.yaml"}},
		},
		{
			name:     "a value of the command's own setting",
			separate: []string{"--admin-addr", "--x"},
			joined:   []string{"--admin-addr=--x"},
			want: commandArgs{
				settings: []string{"--admin-addr=--x"},
				http:     []string{"--admin-addr=--x"},
			},
		},
		{
			name:     "a shorthand keeps its spelling",
			separate: []string{"-n", "api"},
			joined:   []string{"-n=api"},
			want:     commandArgs{http: []string{"-n=api"}},
		},
		{
			name:     "help with a value",
			separate: []string{"--help=true"},
			joined:   []string{"--help=true"},
			want: commandArgs{
				settings: []string{"--help=true"},
				http:     []string{"--help=true"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, splitArgs(tt.separate))
			require.Equal(t, tt.want, splitArgs(tt.joined))
		})
	}
}

func TestRun_AFlagKeepsItsValueWhatever(t *testing.T) {
	// A flag's separate value belongs to its flag, as in Go's flag package, whatever it looks like.
	for _, tt := range []struct {
		name string
		args []string
	}{
		{
			name: "a value that looks like a recipe flag",
			args: []string{"--name", "--recipe-oidc"},
		},
		{
			name: "a recipe flag's value that looks like a setting",
			args: []string{"--recipe-github-auth-user-name", "--config=missing.yaml"},
		},
		{
			name: "the same value after =",
			args: []string{"--recipe-github-auth-user-name=--config=missing.yaml"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withoutDockerHost(t)
			t.Chdir(t.TempDir())

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var stderr bytes.Buffer
			code := run(ctx, append([]string{"--addr", randomAddr(t)}, tt.args...), nil, io.Discard, &stderr)

			require.Equal(t, 0, code, stderr.String())
		})
	}
}

func TestRun_ABoolFlagsSeparateValueIsReportedWithItsFlag(t *testing.T) {
	// "--cors false" sets cors and leaves "false" behind, an error that comes with a hint. The settings that read the
	// flag report it: elsewhere, it would end the flags of other settings, and lose the hint.
	for _, name := range []string{"--recipe-oidc", "--grpc-admin"} {
		t.Run(name, func(t *testing.T) {
			withoutDockerHost(t)
			t.Chdir(t.TempDir())

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var stderr bytes.Buffer
			code := run(ctx, []string{name, "false", "--addr", randomAddr(t), "--log-level", "disabled"}, nil, io.Discard, &stderr)

			require.Equal(t, 2, code, stderr.String())
			require.Contains(t, stderr.String(),
				`unexpected argument "false": only flags are accepted; a bool flag takes its value after =, as in `+name+"=false")
			require.NotContains(t, stderr.String(), "--addr")
		})
	}
}

func TestRun_HelpWithAValueStillHelps(t *testing.T) {
	// Go's flag package reads -help=<anything> as help: another setting that is wrong does not hide it.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"--help=true", "--recipe-oidc-client-id"}, nil, &stdout, &stderr)

	require.Equal(t, 0, code, stderr.String())
	require.Contains(t, stdout.String(), "Usage:")
}

func TestRun_ASubcommandNameCanBeAFlagsValue(t *testing.T) {
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	start(t, "-recipe-github-auth-user-name", "version", "--addr", addr, "--log-level", "disabled")

	target := "http://" + addr + githubauth.UserPath
	firstAnswer(t, target)

	status, body := getWithToken(t, target, githubauth.DefaultToken)
	require.Equal(t, http.StatusOK, status)

	var got githubauth.User
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Equal(t, "version", got.Name)
}

func TestJoinValues_JoinsFlagsBeforeTheCommand(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "a value that names a command",
			args: []string{"-n", "version"},
			want: []string{"-n=version"},
		},
		{
			name: "a bool flag takes no value",
			args: []string{"--debug", "version"},
			want: []string{"--debug", "version"},
		},
		{
			name: "a command's own flags",
			args: []string{"openapi", "--out", "x"},
			want: []string{"openapi", "--out", "x"},
		},
		{
			name: "the terminator",
			args: []string{"-n", "api", "--", "-x"},
			want: []string{"-n=api", "--", "-x"},
		},
		{
			name: "no arguments",
			args: []string{},
			want: []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, joinValues(tt.args))
		})
	}
}

func TestSplitArgs_EachSettingsReadTheirFlagsAsGoDoes(t *testing.T) {
	// Each settings parse their arguments with Go's flag package: whatever a value looks like, and however a flag is
	// spelled, they see every flag addressed to them, with its value, in order, and nothing else. A bool written apart
	// from its bool flag, as in --cors false, ends the flags of that flag's settings only.
	rapid.Check(t, func(t *rapid.T) {
		items := rapid.SliceOfN(genFlagItem(), 0, 8).Draw(t, "flags")

		stray := -1
		var strayItem flagItem
		var strayValue string
		if rapid.Bool().Draw(t, "stray bool") {
			stray = rapid.IntRange(0, len(items)).Draw(t, "stray at")
			strayItem = flagItem{name: rapid.SampledFrom(_boolFlagNames()).Draw(t, "stray flag"), value: "true"}
			strayItem.args = []string{rapid.SampledFrom([]string{"-", "--"}).Draw(t, "stray dash") + strayItem.name}
			strayValue = rapid.SampledFrom(_boolWords).Draw(t, "stray value")
		}

		var args []string
		for i, it := range items {
			if i == stray {
				args = append(args, strayItem.args[0], strayValue)
			}

			args = append(args, it.args...)
		}

		if stray == len(items) {
			args = append(args, strayItem.args[0], strayValue)
		}

		got := splitArgs(args)
		for owner, dst := range map[flagOwner][]string{_toSettings: got.settings, _toHTTP: got.http, _toGRPC: got.grpc, _toRecipe: got.recipe} {
			sets, rest, err := parseAs(owner, dst)
			require.NoError(t, err, "owner %d: %q", owner, dst)

			var want []flagSet
			for i, it := range items {
				if i == stray && strayItem.owners()&owner != 0 {
					want = append(want, strayItem.setFor(owner))
					break
				}

				if it.owners()&owner != 0 {
					want = append(want, it.setFor(owner))
				}
			}

			if stray == len(items) && strayItem.owners()&owner != 0 {
				want = append(want, strayItem.setFor(owner))
			}

			require.Equal(t, want, sets, "owner %d: %q", owner, dst)

			if stray >= 0 && strayItem.owners()&owner != 0 {
				require.NotEmpty(t, rest, "owner %d: %q", owner, dst)
				require.Equal(t, strayValue, rest[0], "owner %d: %q", owner, dst)
			} else {
				require.Empty(t, rest, "owner %d: %q", owner, dst)
			}
		}
	})
}

func TestJoinValues_LeavesNothingForAFlagToTake(t *testing.T) {
	// cobra picks the command from the first argument that isn't a flag: joined, no flag takes the next argument, and
	// the settings read the arguments as they would have before.
	_, takesValue := _flagTable()

	rapid.Check(t, func(t *rapid.T) {
		args := rapid.SliceOfN(genArg(), 0, 10).Draw(t, "args")
		joined := joinValues(args)

		require.Equal(t, splitArgs(args), splitArgs(joined))
		require.Equal(t, joined, joinValues(joined))

		for i := range joined {
			f, ok := readFlag(joined, i, takesValue)
			if !ok {
				break
			}

			require.Equal(t, i, f.last, "%q takes %q", joined[i], joined[f.last])
		}
	})
}

// flagSet is a flag set while parsing, as its settings know it.
type flagSet struct {
	name  string
	value string
}

// recorder is a flag of Go's flag package that records each value it is set to.
type recorder struct {
	name string
	bool bool
	sets *[]flagSet
}

func (r recorder) String() string   { return "" }
func (r recorder) IsBoolFlag() bool { return r.bool }

func (r recorder) Set(v string) error {
	*r.sets = append(*r.sets, flagSet{name: r.name, value: v})
	return nil
}

// parseAs parses args with Go's flag package, as the settings of owner do, with every flag they know.
func parseAs(owner flagOwner, args []string) (sets []flagSet, rest []string, err error) {
	owners, takesValue := _flagTable()

	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	for name, to := range owners {
		if to&owner != 0 {
			fs.Var(recorder{name: name, bool: !takesValue[name], sets: &sets}, name, "")
		}
	}

	// The config file is a hidden setting, which no flag table lists: the command and the HTTP server know it by both
	// names, the others by its long one.
	names := []string{"config"}
	if owner&(_toSettings|_toHTTP) != 0 {
		names = append(names, "c")
	}

	for _, name := range names {
		if fs.Lookup(name) == nil {
			fs.Var(recorder{name: name, sets: &sets}, name, "")
		}
	}

	err = fs.Parse(args)

	return sets, fs.Args(), err
}

// flagItem is a known flag, written in one of its spellings, and the value its settings read.
type flagItem struct {
	args  []string
	name  string
	value string
}

// owners returns the settings the flag is for: all of them for the config file.
func (it flagItem) owners() flagOwner {
	if it.name == "c" || it.name == "config" {
		return _toSettings | _toHTTP | _toGRPC | _toRecipe
	}

	owners, _ := _flagTable()

	return owners[it.name]
}

// setFor returns the flag as owner's settings read it: the gRPC server and the recipes know the config file by its
// long name only.
func (it flagItem) setFor(owner flagOwner) flagSet {
	if it.name == "c" && owner&(_toGRPC|_toRecipe) != 0 {
		return flagSet{name: "config", value: it.value}
	}

	return flagSet{name: it.name, value: it.value}
}

// _boolWords are the words strconv.ParseBool takes.
var _boolWords = []string{"1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE", "false", "False"}

// _trickyValues look like what a value is not: a flag, the terminator, a command, a bool or an assignment.
var _trickyValues = []string{
	"", "-", "--", "---", "-c", "--config", "--recipe-oidc", "--grpc-addr=:1", "-h", "--help", "version", "openapi",
	"false", "true", "a=b", "=", " ",
}

// _flagNames returns the flags of the command but help, whose own handling the property doesn't model.
func _flagNames() []string {
	owners, _ := _flagTable()

	return slices.DeleteFunc(slices.Sorted(maps.Keys(owners)), func(name string) bool { return name == "h" || name == "help" })
}

// _boolFlagNames returns the flags of the command that take no value.
func _boolFlagNames() []string {
	_, takesValue := _flagTable()

	return slices.DeleteFunc(_flagNames(), func(name string) bool { return takesValue[name] })
}

// Generators

func genValue() *rapid.Generator[string] {
	return rapid.OneOf(rapid.SampledFrom(_trickyValues), rapid.String())
}

// genFlagItem generates a known flag: with one dash or two, and a value, if it takes one, apart or after =.
func genFlagItem() *rapid.Generator[flagItem] {
	return rapid.Custom(func(t *rapid.T) flagItem {
		_, takesValue := _flagTable()

		name := rapid.SampledFrom(_flagNames()).Draw(t, "name")
		spelled := rapid.SampledFrom([]string{"-", "--"}).Draw(t, "dash") + name

		switch {
		case takesValue[name] && rapid.Bool().Draw(t, "apart"):
			value := genValue().Draw(t, "value")
			return flagItem{args: []string{spelled, value}, name: name, value: value}
		case takesValue[name]:
			value := genValue().Draw(t, "value")
			return flagItem{args: []string{spelled + "=" + value}, name: name, value: value}
		case rapid.Bool().Draw(t, "explicit"):
			value := rapid.SampledFrom(_boolWords).Draw(t, "value")
			return flagItem{args: []string{spelled + "=" + value}, name: name, value: value}
		default:
			return flagItem{args: []string{spelled}, name: name, value: "true"}
		}
	})
}

// genArg generates an argument of the command: a known flag, alone or with its value, an unknown one, a command, a
// bool or anything else.
func genArg() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Custom(func(t *rapid.T) string {
			return rapid.SampledFrom([]string{"-", "--"}).Draw(t, "dash") + rapid.SampledFrom(_flagNames()).Draw(t, "name")
		}),
		rapid.Custom(func(t *rapid.T) string {
			return rapid.SampledFrom([]string{"-", "--"}).Draw(t, "dash") + rapid.SampledFrom(_flagNames()).Draw(t, "name") +
				"=" + genValue().Draw(t, "value")
		}),
		rapid.SampledFrom([]string{"--recipe-unknown", "--grpc-unknown", "--unknown", "-x", "-help", "--h=false"}),
		genValue(),
	)
}
