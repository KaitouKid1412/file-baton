package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	env := map[string]string{
		"FILE_BATON_AUTO_RESUME":           "false",
		"FILE_BATON_GRANT_TIMEOUT_MINUTES": "2.5",
		"FILE_BATON_COMMIT_GUARD":          "true",
		"FILE_BATON_IDLE_RELEASE_MINUTES":  "-3", // invalid: default kept
		"FILE_BATON_MAX_DIFF_LINES":        "50",
		"CLAUDE_PLUGIN_OPTION_AUTO_RESUME": "true", // not read any more
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

func TestHoldMinutes(t *testing.T) {
	for v, want := range map[string]time.Duration{"": 10 * time.Minute, "0": 0, "2.5": 150 * time.Second, "-1": 10 * time.Minute} {
		c, _ := Load(func(k string) string {
			if k == "FILE_BATON_HOLD_MINUTES" {
				return v
			}
			return ""
		})
		if c.HoldTimeout != want {
			t.Errorf("HOLD_MINUTES=%q: got %v, want %v", v, c.HoldTimeout, want)
		}
	}
}
