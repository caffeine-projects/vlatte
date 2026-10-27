package main

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockgrpc"
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
  vlatte mcp
  vlatte --grpc-descriptors=api.binpb --grpc-mock-files='mocks/grpc/*.yaml'
`
	_helpHint = "Run 'vlatte --help' to list flags, environment variables and file keys."

	// _adminDir is where the command keeps the mocks its admin API adds, across restarts, in the server's work
	// directory. The default mock file patterns don't match it, so only the admin API's store loads its files.
	_adminDir = mockhttp.WorkDirVar + "/_admin"

	// _journalDir is where the command keeps the journal of the requests it serves, across restarts, in the
	// server's work directory.
	_journalDir = mockhttp.WorkDirVar + "/_journal"
)

var (
	//go:embed banner.txt
	_banner      string
	_description = fmt.Sprintf(`%s
Flexible mocking tool for Go.
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

	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the command line in args and returns the exit code:
// 0 on success, 2 for invalid settings or usage, and 1 for other errors.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	root.AddCommand(versionCmd(), mcpCmd(ctx, &code, stdin, stdout, stderr))

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
	s, code, ok := commandSettings(cmd, args, stdout, stderr)
	if !ok {
		return code
	}

	m, g, _, code := startServer(cmd, args, s, stdout, stdout, stderr)
	if m == nil {
		return code
	}

	defer m.Close()
	defer g.Close()

	<-ctx.Done()

	return stopRecording(m, stderr)
}

// serveMCP runs the mock server configured by args, and serves its admin API over MCP on stdin and stdout, until
// stdin ends or ctx is done. stdout is MCP's alone: the server writes its banner and logs to stderr.
func serveMCP(ctx context.Context, cmd *cobra.Command, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	s, code, ok := commandSettings(cmd, args, stdout, stderr)
	if !ok {
		return code
	}

	if !s.admin || !s.mcp {
		_, _ = fmt.Fprintf(stderr, "%s: serves MCP through the admin API: --admin=false and --mcp=false turn it off\n%s\n", cmd.CommandPath(), _helpHint)
		return 2
	}

	m, g, api, code := startServer(cmd, args, s, stderr, stderr, stderr)
	if m == nil {
		return code
	}

	defer m.Close()
	defer g.Close()

	if err := api.ServeMCP(ctx, stdin, stdout); err != nil && ctx.Err() == nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return stopRecording(m, stderr)
}

// commandSettings loads the command's settings from args. On help, or settings with problems, it writes them and
// returns the exit code, and false.
func commandSettings(cmd *cobra.Command, args []string, stdout, stderr io.Writer) (settings, int, bool) {
	s, err := loadSettings(args)
	if err == nil {
		return s, 0, true
	}

	return s, failure(cmd, err, stdout, stderr), false
}

// startServer creates and starts the mock servers of cmd's args, with the command's settings s, which log to
// logOutput, and writes their banner and settings to out: the HTTP server, and the gRPC one, which serves only when
// its settings turn it on. It returns the servers, which the caller closes, and the HTTP server's admin API, or nil
// servers and the exit code.
func startServer(cmd *cobra.Command, args []string, s settings, out, logOutput, stderr io.Writer) (*mockhttp.Server, *mockgrpc.Server, *admin.Admin, int) {
	// Mock files name their on_complete listeners: webhook sends their webhooks, logging with the server's logger and
	// waiting for their answers as long as the webhook.timeout setting says.
	wh := cmp.Or(s.webhook, &webhook.WebHook{})

	httpArgs, grpcArgs := splitArgs(args)

	m, api, err := newServer(httpArgs, s, wh, logOutput)
	if err != nil {
		return nil, nil, nil, failure(cmd, err, out, stderr)
	}

	g, err := newGRPC(grpcArgs, s, wh, logOutput)
	if err != nil {
		// Close releases what New opened, such as the journal.
		m.Close()
		return nil, nil, nil, failure(cmd, err, out, stderr)
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

	// The gRPC server serves when its settings turn it on, after the HTTP one: its mock files can name the same
	// webhooks.
	if err == nil && g.Config().Serve {
		if g.Config().UseTLS {
			err = g.StartTLS()
		} else {
			err = g.Start()
		}
	}

	if err != nil {
		g.Close()
		m.Close()
		_, _ = fmt.Fprintln(stderr, err)

		return nil, nil, nil, 1
	}

	_, _ = fmt.Fprintln(out, _banner)
	_, _ = fmt.Fprintln(out, m.DescribeConfig())

	if g.Config().Serve {
		_, _ = fmt.Fprintf(out, "gRPC mocks: %s, admin API: %s\n", g.Addr(), g.AdminURL())
	}

	return m, g, api, 0
}

// failure reports err, of loading the settings or creating the server, and returns the exit code: 0 for help, which
// it prints for cmd, 2 for settings with problems, and 1 otherwise.
func failure(cmd *cobra.Command, err error, stdout, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		printCommandHelp(cmd, stdout)
		return 0
	}

	_, _ = fmt.Fprintln(stderr, err)

	if _, ok := errors.AsType[*config.Error](err); ok {
		_, _ = fmt.Fprintln(stderr, _helpHint)
		return 2
	}

	return 1
}

// stopRecording stops the recording of m, if one runs, and returns the exit code. A recording writes what it took: a
// call or a mock it couldn't record fails the run. Serving the recordings can fail too, such as for a mock ID a mock
// file holds: the server is exiting, so that is only reported.
func stopRecording(m *mockhttp.Server, stderr io.Writer) int {
	res, err := m.StopRecording(context.Background())
	if err != nil && !errors.Is(err, vlatte.ErrNotRecording) {
		_, _ = fmt.Fprintln(stderr, err)

		if res == nil || res.Status.Failed > 0 {
			return 1
		}
	}

	return 0
}

// printCommandHelp prints the help of cmd to w: the server's, with every setting, for the root command.
func printCommandHelp(cmd *cobra.Command, w io.Writer) {
	if cmd.HasParent() {
		cmd.SetOut(w)
		_ = cmd.Help()

		return
	}

	printHelp(cmd, w)
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
admin or journal section turns the section on. The server keeps its own files in its work directory, %s,
or %s/<name> for a server --name names: --workdir sets another.
The admin API is on, on a random local port: --admin=false turns it off.
It serves AI agents over MCP too, at POST /mcp, and 'vlatte mcp' on stdin and stdout: --mcp=false turns it off.
It keeps the mocks it adds in %s, across restarts: --admin-dir="" keeps them in memory.
The server keeps a journal of the requests it serves in %s, across restarts: --journal=false turns it off.

Setting a key of the grpc section, such as --grpc or --grpc-descriptors, serves gRPC mocks too, on %s: the services of
the descriptor sets --grpc-descriptors names, as the mock files --grpc-mock-files names describe their calls. It
serves gRPC server reflection, and its own admin API, on a random local port, and keeps a journal of its calls, for
as long as it runs: --grpc-reflection=false, --grpc-admin=false and --grpc-journal=false turn them off.

Settings:
%s
gRPC settings:
%s`,
		root.Long, root.Use, root.Use, commands.String(), root.Example,
		mockhttp.DefaultConfigFileName, strings.Join(mockhttp.DefaultConfigDirectories, " or "), mockhttp.EnvPrefix,
		mockhttp.DefaultWorkDir, mockhttp.DefaultWorkDir, _adminDir, _journalDir, _grpcAddr,
		settingsSchema().Usage(mockhttp.EnvPrefix)+mockhttp.ConfigUsage(), mockgrpc.ConfigUsage())
}

// registerCompletions declares the server flags to cobra, which completes their names.
// The bool ones take no value, so cobra can tell flag values from commands.
// cobra leaves flag values to commands that parse their own flags, so the allowed ones are completed here.
func registerCompletions(cmd *cobra.Command) {
	flags := cmd.Flags()
	values := make(map[string][]string)

	for _, f := range slices.Concat(settingsSchema().Flags(), mockhttp.ConfigFlags(), mockgrpc.ConfigFlags()) {
		// The config file is a setting of both servers.
		if flags.Lookup(f.Name) != nil {
			continue
		}

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

// mcpCmd serves the admin API over MCP on stdin and stdout, for an agent that starts vlatte as its MCP server. It
// takes the server's flags, and sets code to its exit code.
func mcpCmd(ctx context.Context, code *int, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "serve the admin API to an AI agent over MCP, on stdin and stdout",
		Long: `Runs the mock server, and serves its admin API to an AI agent over MCP, on stdin and stdout, until stdin ends.
It takes the flags of the server: run 'vlatte --help' to list them. The server writes its banner and logs to stderr.
An agent that runs elsewhere reaches the same tools at POST /mcp of the admin API, which vlatte serves too.`,
		Example:            "  vlatte mcp\n  vlatte mcp --mock-files='mocks/*.yaml'\n",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			*code = serveMCP(ctx, cmd, args, stdin, stdout, stderr)
			return nil
		},
	}
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
