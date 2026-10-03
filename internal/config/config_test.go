package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	env := map[string]string{
		"CLAUDE_PLUGIN_OPTION_AUTO_RESUME":           "false",
		"CLAUDE_PLUGIN_OPTION_GRANT_TIMEOUT_MINUTES": "2.5",
		"CLAUDE_PLUGIN_OPTION_COMMIT_GUARD":          "false",
		"FILE_BATON_COMMIT_GUARD":                    "true", // overrides the plugin option
		"FILE_BATON_IDLE_RELEASE_MINUTES":            "-3",   // invalid: default kept
		"FILE_BATON_MAX_DIFF_LINES":                  "50",
	}
	c, warnings := Load(func(k string) string { return env[k] })
	if c.AutoResume || !c.CommitGuard || c.GrantTimeout != 150*time.Second || c.IdleRelease != 20*time.Minute || c.MaxDiffLines != 50 {
		t.Fatalf("config = %+v", c)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestDefaults(t *testing.T) {
	c, warnings := Load(func(string) string { return "" })
	if c != Default() || len(warnings) != 0 {
		t.Fatalf("config = %+v, warnings = %v", c, warnings)
	}
}
