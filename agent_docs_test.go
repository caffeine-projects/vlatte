package vlatte

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The agent docs send agents to files by link, to sections by the AGENTS.md Rule map and to skills by the AGENTS.md
// Skills table. A link to a moved file, a renamed heading or a renamed skill sends them nowhere, without a word, and a
// skill whose name differs from its folder, or a .claude/skills that no longer links .agents/skills, is never loaded:
// these tests fail instead. A path written as code is not a link, and a section named in prose is not checked.

var (
	// _mdCode matches fenced blocks and code spans, which hold examples, not links.
	_mdCode    = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
	_mdLink    = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	_mdScheme  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	_mdHeading = regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
	_skillName = regexp.MustCompile(`(?m)^name: (.+)$`)
)

const _agentSkillsDir = ".agents/skills"

func TestAgentDocs_LinksResolve(t *testing.T) {
	skills, err := filepath.Glob(filepath.Join(_agentSkillsDir, "*", "SKILL.md"))
	require.NoError(t, err)

	var broken []string
	linking := make(map[string]bool)
	for _, doc := range append([]string{"AGENTS.md", "CONVENTIONS.md", ".agents/README.md"}, skills...) {
		prose := _mdCode.ReplaceAllString(agentDoc(t, doc), "")
		for _, m := range _mdLink.FindAllStringSubmatch(prose, -1) {
			target := m[1]
			if _mdScheme.MatchString(target) || strings.HasPrefix(target, "#") {
				continue
			}

			linking[doc] = true
			path, _, _ := strings.Cut(target, "#")
			if _, err := os.Stat(filepath.Join(filepath.Dir(doc), path)); err != nil {
				broken = append(broken, doc+" links "+target)
			}
		}
	}

	require.Empty(t, broken)
	// Each kind of doc was read and held links: a list that loses one can't pass by checking nothing.
	for _, doc := range []string{"AGENTS.md", "CONVENTIONS.md", ".agents/README.md"} {
		require.True(t, linking[doc], "%s holds no relative link", doc)
	}
}

func TestAgentDocs_RuleMapSendsToSectionsThatExist(t *testing.T) {
	rows := agentDocTable(t, "Rule map")

	var missing []string
	for _, row := range rows {
		file, heading, _ := strings.Cut(row[1], " § ")
		if !slices.Contains(agentDocHeadings(t, file), heading) {
			missing = append(missing, row[1])
		}
	}

	require.Empty(t, missing)
	require.True(t, slices.ContainsFunc(rows, func(row []string) bool { return row[1] == "AGENTS.md § Agent docs" }),
		"the Rule map sends no agent doc edit to AGENTS.md § Agent docs")
}

func TestAgentDocs_SkillsTableLoadsSkillsThatExist(t *testing.T) {
	rows := agentDocTable(t, "Skills")
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, strings.Trim(row[1], "`"))
	}

	var missing []string
	for _, name := range names {
		if skillName(filepath.Join(_agentSkillsDir, name, "SKILL.md")) != name {
			missing = append(missing, name)
		}
	}

	require.Empty(t, missing)
	// Rules 13 and 14 send every change through these two.
	require.Subset(t, names, []string{"plan-change", "review-change"})
}

func TestAgentDocs_SkillNamesMatchTheirFolders(t *testing.T) {
	// The Agent Skills format loads a skill only when its name is its folder's.
	entries, err := os.ReadDir(_agentSkillsDir)
	require.NoError(t, err)

	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
			require.Equal(t, e.Name(), skillName(filepath.Join(_agentSkillsDir, e.Name(), "SKILL.md")))
		}
	}

	require.Subset(t, folders, []string{"plan-change", "review-change"})
}

func TestAgentDocs_ClaudeSkillsLinksAgentSkills(t *testing.T) {
	// Claude Code reads .claude/skills; the other agents read .agents/skills. A clone without symlinks checks the link
	// out as a plain file, and Claude Code loads no skill.
	target, err := os.Readlink(".claude/skills")
	require.NoError(t, err)
	require.Equal(t, "../"+_agentSkillsDir, filepath.ToSlash(target))
}

func TestAgentDocs_ClaudeImportsTheRules(t *testing.T) {
	// Claude Code reads CLAUDE.md, which imports the rule files the other agents read directly. A lost or broken import
	// drops those rules from Claude Code without a word.
	var imports []string
	for line := range strings.SplitSeq(agentDoc(t, "CLAUDE.md"), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "@"); ok {
			imports = append(imports, path)
			require.FileExists(t, path)
		}
	}

	require.Subset(t, imports, []string{"AGENTS.md", "CONVENTIONS.md"})
}

// agentDoc returns file with LF line ends, which a Windows checkout turns into CRLF.
func agentDoc(t *testing.T, file string) string {
	t.Helper()

	b, err := os.ReadFile(file)
	require.NoError(t, err)

	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// agentDocTable returns the body of the table in the section of AGENTS.md headed "## <section>": the trimmed cells of
// each row below the separator.
func agentDocTable(t *testing.T, section string) [][]string {
	t.Helper()

	_, body, ok := strings.Cut(agentDoc(t, "AGENTS.md"), "\n## "+section+"\n")
	require.True(t, ok, "AGENTS.md has no section %q", section)
	body, _, _ = strings.Cut(body, "\n## ")

	var rows [][]string
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}

		rows = append(rows, cells)
	}

	require.GreaterOrEqual(t, len(rows), 2, "the %s section of AGENTS.md holds no table", section)

	return rows[2:]
}

func agentDocHeadings(t *testing.T, file string) []string {
	t.Helper()

	matches := _mdHeading.FindAllStringSubmatch(agentDoc(t, file), -1)
	titles := make([]string, 0, len(matches))
	for _, m := range matches {
		titles = append(titles, strings.TrimSpace(m[1]))
	}

	return titles
}

// skillName returns the name the SKILL.md at path declares, or "" when it can't be read or declares none.
func skillName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	m := _skillName.FindSubmatch(b)
	if m == nil {
		return ""
	}

	return strings.TrimSpace(string(m[1]))
}
