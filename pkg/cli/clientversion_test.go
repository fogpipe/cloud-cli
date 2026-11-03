package cli

import "testing"

func TestClientVersionBelow(t *testing.T) {
	cases := []struct {
		version, floor string
		below          bool
	}{
		{"v0.242.0-1-gd522208", "v0.242.0", false},
		{"v0.242.0-1-gd522208-dirty", "v0.242.0", false},
		{"v0.242.0-dirty", "v0.242.0", false},
		{"v0.241.9-3-gabc1234", "v0.242.0", true},
		{"v0.242.0", "v0.242.0", false},
		{"v0.241.0", "v0.242.0", true},
		{"v1.0.0-rc.1", "v1.0.0", true},
		{"dev", "v0.242.0", false},
		{"v0.242.0", "", false},
	}
	for _, c := range cases {
		if got := clientVersionBelow(c.version, c.floor); got != c.below {
			t.Errorf("clientVersionBelow(%q, %q) = %v, want %v", c.version, c.floor, got, c.below)
		}
	}
}
