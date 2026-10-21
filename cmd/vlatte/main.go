package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

const (
	_dockerHostEnv    = "VLATTE_DOCKER_HOST"
	_gitRepository    = "https://github.com/caffeine-projects/vlatte"
	_usage            = "vlatte"
	_shortDescription = "Build Mock APIs in Go"
	_example          = `  vlatte
  vlatte --addr=:3000
  vlatte --proxy
  vlatte --proxy --proxy-intercept --proxy-ca-cert=ca.pem --proxy-ca-key=ca-key.pem
  vlatte --proxy-all-target=https://api.example.com
  vlatte --proxy --record
  vlatte --record --record-target=https://api.example.com
  vlatte --admin-addr=127.0.0.1:8081
`
	_helpHint = "Run 'vlatte --help' to list flags, environment variables and file keys."

	// _adminDir is where the command keeps the mocks its admin API adds, across restarts. The default mock file
	// patterns don't match it, so only the admin API's store loads its files.
	_adminDir = "testdata/_admin"

	// _journalDir is where the command keeps the journal of the requests it serves, across restarts.
	_journalDir = "testdata/_journal"
)

var (
	//go:embed banner.txt
	_banner      string
	_description = fmt.Sprintf(`%s
Flexible HTTP mocking tool for Go.
Mock file formats: %s

For more information, visit: %s`,
		_banner,
		strings.Join(mockhttp.MockFileFormats(), ", "),
		_gitRepository,
	)
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// A signal closes the server, which then writes what it recorded: stop gives the signals back, so another one ends
	// the process at once.
	context.AfterFunc(ctx, stop)

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the command line in args and returns the exit code:
// 0 on success, 2 for invalid settings or usage, and 1 for other errors.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	code := 0

	root := &cobra.Command{
		Use:     _usage,
		Short:   _shortDescription,
		Long:    _description,
		Example: _example,
		// The config args source parses the flags, so they behave the same here and in the library.
		// cobra only knows them for shell completion.
		DisableFlagParsing:         true,
		Args:                       cobra.ArbitraryArgs,
		SilenceUsage:               true,
		SilenceErrors:              true,
		SuggestionsMinimumDistance: 2,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The server takes no arguments, so a leading word is a mistyped command.
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				return unknownCommand(cmd, args[0])
			}

			code = serve(ctx, cmd, args, stdout, stderr)

			return nil
		},
	}

	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd != root {
			defaultHelp(cmd, args)
			return
		}

		printHelp(root, cmd.OutOrStdout())
	})

	registerCompletions(root)
	root.AddCommand(versionCmd())

	// cobra reads os.Args when the args are nil.
	if args == nil {
		args = []string{}
	}

	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n%s\n", err, _helpHint)
		return 2
	}

	return code
}

// serve runs the mock server configured by args until ctx is done.
func serve(ctx context.Context, cmd *cobra.Command, args []string, stdout, stderr io.Writer) int {
	// Mock files name their on_complete listeners: webhook sends their webhooks, logging with the server's logger and
	// waiting for their answers as long as the server's webhook.timeout setting says.
	wh := &webhook.WebHook{}

	// The command serves the admin API, which keeps the mocks it adds across restarts, and keeps a journal of the
	// requests it serves, unless settings turn them off: they apply after code.
	m, err := mockhttp.New(mockhttp.Setup().
		Admin(adminAddr()).
		AdminStore(mockhttp.FileStore(_adminDir)).
		Journal(_journalDir).
		ConfigSources(mockhttp.DefaultConfigSources(args)...).
		OnCompleteListener("webhook", wh))
	if errors.Is(err, flag.ErrHelp) {
		printHelp(cmd, stdout)
		return 0
	}

	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		if _, ok := errors.AsType[*config.Error](err); ok {
			_, _ = fmt.Fprintln(stderr, _helpHint)
			return 2
		}

		return 1
	}

	// The docker address wins over every source, and its port depends on the final https setting.
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		_ = (&dockerConfigurer{}).Apply(m.Config())
	}

	if m.Config().UseHTTPS {
		err = m.StartTLS()
	} else {
		err = m.Start()
	}

	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	defer m.Close()

	_, _ = fmt.Fprintln(stdout, _banner)
	_, _ = fmt.Fprintln(stdout, m.DescribeConfig())

	<-ctx.Done()

	// A recording writes what it took: a call or a mock it couldn't record fails the run. Serving the recordings can
	// fail too, such as for a mock ID a mock file holds: the server is exiting, so that is only reported.
	res, err := m.StopRecording(context.Background())
	if err != nil && !errors.Is(err, vlatte.ErrNotRecording) {
		_, _ = fmt.Fprintln(stderr, err)

		if res == nil || res.Status.Failed > 0 {
			return 1
		}
	}

	return 0
}

// printHelp describes the server command: its usage, commands, and every setting with the ways to set it.
func printHelp(root *cobra.Command, w io.Writer) {
	var commands strings.Builder
	for _, cmd := range root.Commands() {
		if cmd.IsAvailableCommand() {
			_, _ = fmt.Fprintf(&commands, "  %-11s %s\n", cmd.Name(), cmd.Short)
		}
	}

	_, _ = fmt.Fprintf(w, `%s

Usage:
  %s [flags]
  %s [command]

Commands:
%s
Examples:
%s
Settings apply in this order, each overriding the ones before: defaults, the %s.{json,yaml,yml} file
in %s, %s_* environment variables, then flags. Setting a key of the cors, proxy, proxy_all, record,
admin or journal section turns the section on. The admin API is on, on a random local port: --admin=false turns it off.
It keeps the mocks it adds in %s, across restarts: --admin-dir="" keeps them in memory.
The server keeps a journal of the requests it serves in %s, across restarts: --journal=false turns it off.

Settings:
%s`,
		root.Long, root.Use, root.Use, commands.String(), root.Example,
		mockhttp.DefaultConfigFileName, strings.Join(mockhttp.DefaultConfigDirectories, " or "), mockhttp.EnvPrefix,
		_adminDir, _journalDir, mockhttp.ConfigUsage())
}

// registerCompletions declares the server flags to cobra, which completes their names.
// The bool ones take no value, so cobra can tell flag values from commands.
// cobra leaves flag values to commands that parse their own flags, so the allowed ones are completed here.
func registerCompletions(cmd *cobra.Command) {
	flags := cmd.Flags()
	values := make(map[string][]string)

	for _, f := range mockhttp.ConfigFlags() {
		if f.Bool {
			flags.BoolP(f.Name, f.Shorthand, false, f.Usage)
		} else {
			flags.StringP(f.Name, f.Shorthand, "", f.Usage)
		}

		if len(f.Values) > 0 {
			values[f.Name] = f.Values
		}
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return completeFlagValue(values, args, toComplete)
	}
}

// completeFlagValue completes the value of a flag with allowed values, as in "--log-level <TAB>" or "--log-level=<TAB>".
// Other values fall back to the shell's file completion, which suits paths.
func completeFlagValue(values map[string][]string, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var name, prefix string

	switch flagName, value, ok := strings.Cut(toComplete, "="); {
	case ok && strings.HasPrefix(flagName, "-"):
		name, prefix, toComplete = strings.TrimLeft(flagName, "-"), flagName+"=", value
	case strings.HasPrefix(toComplete, "-"):
		// cobra completes flag names.
		return nil, cobra.ShellCompDirectiveNoFileComp
	case len(args) > 0 && strings.HasPrefix(args[len(args)-1], "-"):
		name = strings.TrimLeft(args[len(args)-1], "-")
	}

	allowed, ok := values[name]
	if !ok {
		return nil, cobra.ShellCompDirectiveDefault
	}

	var completions []cobra.Completion
	for _, v := range allowed {
		if strings.HasPrefix(v, toComplete) {
			completions = append(completions, prefix+v)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func unknownCommand(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(suggestions, "\n\t")
	}

	return errors.New(msg)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print version info",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version())
		},
	}
}
