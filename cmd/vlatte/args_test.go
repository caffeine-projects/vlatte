package main

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

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
