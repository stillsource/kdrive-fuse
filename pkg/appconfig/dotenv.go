package appconfig

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sethvargo/go-envconfig"
)

// dotEnvFileVar names the environment variable that overrides which file the
// auto-loader reads (default: ".env" in the working directory).
const dotEnvFileVar = "KDRIVE_ENV_FILE"

// dotEnvVars discovers and parses the optional .env file. It reads the path in
// KDRIVE_ENV_FILE when set (a missing file there is an error — the user asked
// for it explicitly), otherwise ".env" in the working directory (a missing one
// is fine, auto-load is best-effort). Returns nil when there is nothing to load.
func dotEnvVars() (map[string]string, error) {
	if p := os.Getenv(dotEnvFileVar); p != "" {
		return readEnvFile(p, true)
	}
	return readEnvFile(".env", false)
}

// readEnvFile parses the env file at path. When explicit is false and the file
// does not exist, it returns (nil, nil) rather than an error.
func readEnvFile(path string, explicit bool) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("env file %q: %w", path, err)
	}
	return parseDotEnv(string(data))
}

// parseDotEnv parses KEY=VALUE lines (systemd EnvironmentFile style). Blank
// lines and lines whose first non-space character is '#' are ignored; a leading
// "export " is stripped. The value is taken literally after trimming surrounding
// whitespace and a single layer of matching quotes — inline "# comments" are NOT
// stripped (a '#' can be a legitimate value character), matching systemd. A line
// without '=' or with an empty key is an error so a typo fails loudly.
func parseDotEnv(content string) (map[string]string, error) {
	m := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(content))
	for line := 0; sc.Scan(); {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		rawKey, rawVal, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE, got %q", line, text)
		}
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		m[key] = unquote(strings.TrimSpace(rawVal))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// unquote strips one layer of matching single or double quotes, if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if c := s[0]; (c == '"' || c == '\'') && s[len(s)-1] == c {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// lookuperFor returns the Lookuper used to populate the Config: the OS
// environment alone, or — when a .env supplied values — the OS environment
// layered over those file values. The OS comes first so a real environment
// variable always overrides the file (e.g. systemd's EnvironmentFile, or an
// ad-hoc `KDRIVE_X=… kdrive …`).
func lookuperFor(fileVars map[string]string) envconfig.Lookuper {
	if len(fileVars) == 0 {
		return envconfig.OsLookuper()
	}
	return envconfig.MultiLookuper(envconfig.OsLookuper(), envconfig.MapLookuper(fileVars))
}
