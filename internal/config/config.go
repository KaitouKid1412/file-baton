// Package config reads file-baton's settings from the environment.
//
// Plugin userConfig values arrive as CLAUDE_PLUGIN_OPTION_<KEY>; FILE_BATON_<KEY>
// overrides them, which is handy for tests and for switching the plugin off.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the effective configuration.
type Config struct {
	AutoResume   bool
	CommitGuard  bool
	GrantTimeout time.Duration
	IdleRelease  time.Duration
	MaxDiffLines int
	Disabled     bool
}

// Default is the configuration with nothing set.
func Default() Config {
	return Config{
		AutoResume:   true,
		CommitGuard:  true,
		GrantTimeout: 10 * time.Minute,
		IdleRelease:  20 * time.Minute,
		MaxDiffLines: 200,
	}
}

// Load builds the configuration from getenv and returns warnings for values it
// could not use.
func Load(getenv func(string) string) (Config, []string) {
	c := Default()
	var warnings []string
	lookup := func(key string) string {
		if v := getenv("FILE_BATON_" + key); v != "" {
			return v
		}
		return getenv("CLAUDE_PLUGIN_OPTION_" + key)
	}
	boolean := func(key string, dst *bool) {
		v := lookup(key)
		if v == "" {
			return
		}
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s=%q is not a boolean; using %v", key, v, *dst))
			return
		}
		*dst = b
	}
	minutes := func(key string, dst *time.Duration) {
		v := lookup(key)
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f <= 0 {
			warnings = append(warnings, fmt.Sprintf("%s=%q is not a positive number; using %v", key, v, *dst))
			return
		}
		*dst = time.Duration(f * float64(time.Minute))
	}
	boolean("AUTO_RESUME", &c.AutoResume)
	boolean("COMMIT_GUARD", &c.CommitGuard)
	boolean("DISABLED", &c.Disabled)
	minutes("GRANT_TIMEOUT_MINUTES", &c.GrantTimeout)
	minutes("IDLE_RELEASE_MINUTES", &c.IdleRelease)
	if v := lookup("MAX_DIFF_LINES"); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			warnings = append(warnings, fmt.Sprintf("MAX_DIFF_LINES=%q is not a non-negative integer; using %d", v, c.MaxDiffLines))
		} else {
			c.MaxDiffLines = n
		}
	}
	return c, warnings
}
