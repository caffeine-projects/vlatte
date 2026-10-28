package mockhttp

import (
	"errors"
	"fmt"
	"maps"
)

// errProblemMember is the error of a problem extension named after a standard member.
var errProblemMember = errors.New("problem: an extension can't be named after a standard member")

// Problem builds an RFC 9457 problem details object, the body [Reply.Problem] sends. Every member is optional: one
// that is never set is left out of the JSON, and a missing type means "about:blank" to clients.
type Problem struct {
	typ        string
	title      string
	status     int
	detail     string
	instance   string
	extensions map[string]any
}

// NewProblem creates a new, empty Problem.
func NewProblem() *Problem {
	return &Problem{}
}

// Type sets the URI reference that identifies the problem type.
func (p *Problem) Type(uri string) *Problem {
	p.typ = uri
	return p
}

// Title sets the short, human-readable summary of the problem type.
func (p *Problem) Title(title string) *Problem {
	p.title = title
	return p
}

// Status sets the HTTP status code of the problem. [Reply.Problem] gives the response this status too. Zero leaves
// it out.
func (p *Problem) Status(status int) *Problem {
	p.status = status
	return p
}

// Detail sets the human-readable explanation of this occurrence of the problem.
func (p *Problem) Detail(detail string) *Problem {
	p.detail = detail
	return p
}

// Instance sets the URI reference that identifies this occurrence of the problem.
func (p *Problem) Instance(uri string) *Problem {
	p.instance = uri
	return p
}

// Extension sets an extension member, encoded as JSON next to the standard members. Setting a name again replaces
// its value. A name of a standard member (type, title, status, detail or instance) makes building the mock that
// replies with the problem fail.
func (p *Problem) Extension(name string, value any) *Problem {
	if p.extensions == nil {
		p.extensions = make(map[string]any)
	}

	p.extensions[name] = value

	return p
}

// members returns the members of p to encode as JSON: the standard members set and the extensions.
func (p *Problem) members() (map[string]any, error) {
	members := make(map[string]any)
	if p == nil {
		return members, nil
	}

	for _, m := range []struct {
		name string
		set  bool
		v    any
	}{
		{"type", p.typ != "", p.typ},
		{"title", p.title != "", p.title},
		{"status", p.status != 0, p.status},
		{"detail", p.detail != "", p.detail},
		{"instance", p.instance != "", p.instance},
	} {
		if _, ok := p.extensions[m.name]; ok {
			return nil, fmt.Errorf("%w: %q", errProblemMember, m.name)
		}

		if m.set {
			members[m.name] = m.v
		}
	}

	maps.Copy(members, p.extensions)

	return members, nil
}
