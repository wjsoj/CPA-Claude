package config

import "testing"

// The zero value of the config struct is what an operator who has never heard
// of this key gets, so the default has to be readable straight off it.
func TestFableOAuthEnabledDefaultsOn(t *testing.T) {
	var c Config
	if !c.FableOAuthEnabled() {
		t.Fatal("absent anthropic_fable_oauth must leave fable on the OAuth pool")
	}
}

func TestFableOAuthEnabledHonoursExplicitValues(t *testing.T) {
	for _, tc := range []struct{ set, want bool }{{true, true}, {false, false}} {
		v := tc.set
		c := Config{AnthropicFableOAuth: &v}
		if got := c.FableOAuthEnabled(); got != tc.want {
			t.Errorf("anthropic_fable_oauth=%v → FableOAuthEnabled()=%v, want %v", tc.set, got, tc.want)
		}
	}
}
