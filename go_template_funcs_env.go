package vlatte

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
	"text/template/parse"
)

// errEnvNotAllowed says a template read an environment variable the engine doesn't allow.
var errEnvNotAllowed = errors.New("is not allowed")

// WithEnv allows templates to read the environment variables names with env. Names are exact and case-sensitive, and
// each call adds to the names allowed. Without it, templates read none: env fails for every name.
func WithEnv(names ...string) GoTemplateOption {
	return func(e *GoTemplateEngine) error {
		for _, name := range names {
			if name == "" || strings.ContainsAny(name, "=\x00") {
				return fmt.Errorf("environment variable name %q is invalid", name)
			}

			e.envAllowed[name] = true
		}

		return nil
	}
}

// env returns the value of the environment variable name, "" when it is unset, if the engine allows it.
func (e *GoTemplateEngine) env(name string) (string, error) {
	if !e.envAllowed[name] {
		return "", fmt.Errorf("%q %w", name, errEnvNotAllowed)
	}

	return os.Getenv(name), nil
}

// checkEnv returns the error env would fail with at execution for the first name it is given as a literal in t and
// doesn't allow, so the template fails when it compiles. Names env reads from data are checked when it runs.
func (e *GoTemplateEngine) checkEnv(t *template.Template) error {
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}

		var err error

		walkPipes(tt.Root, func(p *parse.PipeNode) bool {
			for _, name := range envLiterals(p) {
				if _, err = e.env(name); err != nil {
					return false
				}
			}

			return true
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// envLiterals returns the names env is given as string literals in a pipeline: {{ env "NAME" }} and
// {{ "NAME" | env }}.
func envLiterals(p *parse.PipeNode) []string {
	var names []string

	for i, cmd := range p.Cmds {
		if len(cmd.Args) == 0 {
			continue
		}

		if id, ok := cmd.Args[0].(*parse.IdentifierNode); !ok || id.Ident != "env" {
			continue
		}

		var arg parse.Node

		switch {
		case len(cmd.Args) > 1:
			arg = cmd.Args[1]
		case i > 0 && len(p.Cmds[i-1].Args) == 1:
			arg = p.Cmds[i-1].Args[0]
		}

		if s, ok := arg.(*parse.StringNode); ok {
			names = append(names, s.Text)
		}
	}

	return names
}

// walkPipes calls visit for each pipeline in the tree of n, parenthesized ones included, until visit returns false.
// It returns false once visit did.
func walkPipes(n parse.Node, visit func(*parse.PipeNode) bool) bool {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return true
		}

		for _, c := range n.Nodes {
			if !walkPipes(c, visit) {
				return false
			}
		}
	case *parse.ActionNode:
		return walkPipes(n.Pipe, visit)
	case *parse.TemplateNode:
		return walkPipes(n.Pipe, visit)
	case *parse.IfNode:
		return walkBranch(&n.BranchNode, visit)
	case *parse.RangeNode:
		return walkBranch(&n.BranchNode, visit)
	case *parse.WithNode:
		return walkBranch(&n.BranchNode, visit)
	case *parse.ChainNode:
		return walkPipes(n.Node, visit)
	case *parse.PipeNode:
		if n == nil {
			return true
		}

		if !visit(n) {
			return false
		}

		for _, cmd := range n.Cmds {
			for _, arg := range cmd.Args {
				if !walkPipes(arg, visit) {
					return false
				}
			}
		}
	}

	return true
}

func walkBranch(b *parse.BranchNode, visit func(*parse.PipeNode) bool) bool {
	return walkPipes(b.Pipe, visit) && walkPipes(b.List, visit) && walkPipes(b.ElseList, visit)
}
