package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// loadFile loads the file source into a fresh config.
func loadFile(t *testing.T, sources ...Source) (testConfig, map[string]Value, error) {
	t.Helper()

	var c testConfig
	values, err := testSchema().Load(&c, sources)

	return c, values, err
}

func TestFile_JSONAndYAMLGiveTheSameSettings(t *testing.T) {
	// The format is the user's choice: the same settings must load the same way from every extension.
	dir := t.TempDir()
	yamlDoc := "name: api\nport: 8080\ndebug: true\nlog:\n  level: info\n  timeout: 1500\ntags: [a, b]\nfeature:\n  size: 3\n"
	files := []string{
		writeFile(t, dir, "a.json", `{"name": "api", "port": 8080, "debug": true, "log": {"level": "info", "timeout": 1500}, "tags": ["a", "b"], "feature": {"size": 3}}`),
		writeFile(t, dir, "a.yaml", yamlDoc),
		writeFile(t, dir, "a.yml", yamlDoc),
	}

	want := testConfig{
		Name:    "api",
		Port:    8080,
		Debug:   true,
		Level:   1,
		Timeout: 1500 * time.Millisecond,
		Tags:    []string{"a", "b"},
		Feature: &testFeature{Size: 3, Mode: "fast"},
	}

	for _, path := range files {
		c, values, err := loadFile(t, File("config", path))

		require.NoError(t, err, path)
		require.Equal(t, want, c, path)
		require.Equal(t, "file "+path, values["name"].Origin, path)
	}
}

func TestFile_SectionForms(t *testing.T) {
	// A section is turned on by true or by an object, with defaults for the keys it doesn't set.
	tests := []struct {
		doc  string
		want *testFeature
	}{
		{`{"feature": false}`, nil},
		{`{"feature": true}`, &testFeature{Size: 10, Mode: "fast"}},
		{`{"feature": {}}`, &testFeature{Size: 10, Mode: "fast"}},
		{`{"feature": {"mode": "slow"}}`, &testFeature{Size: 10, Mode: "slow"}},
	}

	for _, tc := range tests {
		c, _, err := loadFile(t, File("config", writeFile(t, t.TempDir(), "a.json", tc.doc)))

		require.NoError(t, err, tc.doc)
		require.Equal(t, tc.want, c.Feature, tc.doc)
	}
}

func TestFile_EmptyFilesSetNothing(t *testing.T) {
	dir := t.TempDir()

	for _, path := range []string{
		writeFile(t, dir, "a.json", " \n"),
		writeFile(t, dir, "a.yaml", ""),
		writeFile(t, dir, "b.yaml", "# all settings are commented out\n# name: api\n"),
	} {
		c, values, err := loadFile(t, File("config", path))

		require.NoError(t, err, path)
		require.Empty(t, values, path)
		require.Equal(t, testConfig{}, c, path)
	}
}

func TestFile_Problems(t *testing.T) {
	// Every problem in a file names the file and the key, and says how to fix it.
	dir := t.TempDir()

	tests := []struct {
		name string
		file string
		doc  string
		want []Problem
	}{
		{
			name: "unknown keys",
			file: "a.json",
			doc:  `{"nmae": "api", "log": {"levl": 1}, "feature": {"colour": "red"}}`,
			want: []Problem{
				{Code: CodeUnknownKey, Key: "feature.colour", Message: "feature.colour is not a known setting", Hint: "the keys under feature are size, mode"},
				{Code: CodeUnknownKey, Key: "log.levl", Message: "log.levl is not a known setting", Hint: "did you mean log.level?"},
				{Code: CodeUnknownKey, Key: "nmae", Message: "nmae is not a known setting", Hint: "did you mean name?"},
			},
		},
		{
			name: "dotted key",
			file: "b.json",
			doc:  `{"log.level": 1}`,
			want: []Problem{{Code: CodeUnknownKey, Key: "log.level", Message: "log.level is written as a dotted key", Hint: "nest it: log: {level: ...}"}},
		},
		{
			name: "group that isn't an object",
			file: "c.yaml",
			doc:  "log: 5\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "log", Value: "5", Message: "log must be an object, not a number", Hint: "its keys are level, timeout"}},
		},
		{
			name: "path key inside a file",
			file: "d.json",
			doc:  `{"config": "other.json"}`,
			want: []Problem{{Code: CodeNotAllowed, Key: "config", Message: "config can't be set inside a config file", Hint: "set it with a flag or an environment variable"}},
		},
		{
			name: "list at the top",
			file: "e.json",
			doc:  `[1, 2]`,
			want: []Problem{{Code: CodeParseError, Message: "a config file holds an object of settings, not a list"}},
		},
		{
			name: "group set to a bool",
			file: "f.yaml",
			doc:  "log: true\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "log", Value: "true", Message: "log must be an object, not a bool", Hint: "its keys are level, timeout"}},
		},
		{
			name: "text at the top",
			file: "g.yaml",
			doc:  "name\n",
			want: []Problem{{Code: CodeParseError, Message: "a config file holds an object of settings, not a string"}},
		},
		{
			// YAML keys are text, as in JSON: a number key is an unknown setting, not a decoding failure.
			name: "number keys are text",
			file: "h.yaml",
			doc:  "1: a\n",
			want: []Problem{{Code: CodeUnknownKey, Key: "1", Message: "1 is not a known setting"}},
		},
		{
			name: "keys that aren't text",
			file: "i.yaml",
			doc:  "[a, b]: c\n",
			want: []Problem{{Code: CodeParseError, Message: "yaml: line 1: a mapping key must be text"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, dir, tc.file, tc.doc)
			_, _, err := loadFile(t, File("config", path))

			for i := range tc.want {
				tc.want[i].Origin = "file " + path
			}

			require.Equal(t, tc.want, requireProblems(t, err))
		})
	}
}

func TestFile_SyntaxErrorsPointToTheLine(t *testing.T) {
	dir := t.TempDir()

	t.Run("json", func(t *testing.T) {
		path := writeFile(t, dir, "a.json", "{\n  \"name\": \"api\",\n  \"port\": \n}\n")
		_, _, err := loadFile(t, File("config", path))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeParseError, p.Code)
		require.Equal(t, "file "+path+":4:1", p.Origin)
	})

	t.Run("yaml duplicate key", func(t *testing.T) {
		path := writeFile(t, dir, "a.yaml", "name: api\nname: web\n")
		_, _, err := loadFile(t, File("config", path))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeParseError, p.Code)
		require.Equal(t, "file "+path, p.Origin)
		require.Contains(t, p.Message, `line 2: mapping key "name" already defined at line 1`)
	})
}

func TestFile_ExplicitPath(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, _, err := loadFile(t, File("config", "missing.json"))

		wd, _ := os.Getwd()
		require.Equal(t, []Problem{{
			Code:    CodeFileNotFound,
			Origin:  "file missing.json",
			Message: `config file "missing.json" does not exist`,
			Hint:    "relative paths start at " + wd,
		}}, requireProblems(t, err))
	})

	t.Run("unsupported format", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "a.properties", "name=api\n")
		_, _, err := loadFile(t, File("config", path))

		require.Equal(t, []Problem{{
			Code:    CodeUnsupportedFormat,
			Origin:  "file " + path,
			Message: `config file "` + path + `" is not JSON or YAML`,
			Hint:    "use a .json, .yaml or .yml file",
		}}, requireProblems(t, err))
	})
}

func TestFile_ReadErrors(t *testing.T) {
	// A path that exists but can't be read is reported, never skipped: skipping would drop the user's settings.
	dir := t.TempDir()

	t.Run("a lookup dir that is a file", func(t *testing.T) {
		notDir := writeFile(t, dir, "not-a-dir", "")
		_, _, err := loadFile(t, File("config", ".app", notDir))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeReadError, p.Code)
		require.Equal(t, "file "+notDir, p.Origin)
	})

	t.Run("a config file that is a dir", func(t *testing.T) {
		path := filepath.Join(dir, "conf.yaml")
		require.NoError(t, os.Mkdir(path, 0o700))

		_, _, err := loadFile(t, File("config", path))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeReadError, p.Code)
		require.Equal(t, "file "+path, p.Origin)
	})
}

func TestFile_Lookup(t *testing.T) {
	t.Run("nothing found is fine", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, ".app.json.bak", `{"name": "backup"}`) // a different name

		c, values, err := loadFile(t, File("config", ".app", dir, filepath.Join(dir, "missing")))

		require.NoError(t, err)
		require.Empty(t, values)
		require.Equal(t, testConfig{}, c)
	})

	t.Run("the first dir with a file wins", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		writeFile(t, first, ".app.yaml", "name: first\n")
		writeFile(t, second, ".app.json", `{"name": "second"}`)

		c, _, err := loadFile(t, File("config", ".app", filepath.Join(first, "missing"), first, second))

		require.NoError(t, err)
		require.Equal(t, "first", c.Name)
	})

	t.Run("two files in one dir are ambiguous", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, ".app.json", `{"name": "json"}`)
		writeFile(t, dir, ".app.yaml", "name: yaml\n")

		_, _, err := loadFile(t, File("config", ".app", dir))

		require.Equal(t, []Problem{{
			Code:    CodeFileAmbiguous,
			Origin:  "file " + dir,
			Message: "found .app.json and .app.yaml in " + dir,
			Hint:    "keep only one of them",
		}}, requireProblems(t, err))
	})

	t.Run("an unsupported format is not skipped", func(t *testing.T) {
		// A .toml file used to load; ignoring it silently would drop the user's settings.
		dir := t.TempDir()
		path := writeFile(t, dir, ".app.toml", "name = 'api'\n")

		_, _, err := loadFile(t, File("config", ".app", dir))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeUnsupportedFormat, p.Code)
		require.Equal(t, "file "+path, p.Origin)
	})
}

func TestFile_PathFromHigherSources(t *testing.T) {
	// The path key lets flags and env pick the file, with the usual precedence: flag, then env, then lookup.
	dir := t.TempDir()
	writeFile(t, dir, ".app.yaml", "name: lookup\n")
	fromEnv := writeFile(t, dir, "env.yaml", "name: env\n")
	fromFlag := writeFile(t, dir, "flag.yaml", "name: flag\n")
	lookupFile := File("config", ".app", dir)

	t.Setenv("APP_CONFIG", fromEnv)

	c, values, err := loadFile(t, lookupFile, Env("APP"), Args([]string{"--config", fromFlag}))
	require.NoError(t, err)
	require.Equal(t, "flag", c.Name)
	require.Equal(t, "file "+fromFlag, values["name"].Origin)

	c, _, err = loadFile(t, lookupFile, Env("APP"))
	require.NoError(t, err)
	require.Equal(t, "env", c.Name)

	t.Run("a missing file names who asked for it", func(t *testing.T) {
		_, _, err := loadFile(t, lookupFile, Args([]string{"-c", filepath.Join(dir, "missing.yaml")}))

		p := requireProblems(t, err)[0]
		require.Equal(t, CodeFileNotFound, p.Code)
		require.Equal(t, "flag -c", p.Origin)
	})

	t.Run("an explicit file is never redirected", func(t *testing.T) {
		explicit := writeFile(t, dir, "explicit.yaml", "name: explicit\n")

		c, _, err := loadFile(t, File("config", explicit), Args([]string{"--config", fromFlag}))
		require.NoError(t, err)
		require.Equal(t, "explicit", c.Name)
	})

	t.Run("a lower source can't steer a higher one", func(t *testing.T) {
		// Here the file outranks env, so APP_CONFIG must not pick its path.
		c, _, err := loadFile(t, Env("APP"), lookupFile)
		require.NoError(t, err)
		require.Equal(t, "lookup", c.Name)
	})
}
