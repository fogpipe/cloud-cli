package cli

import "testing"

// A quantity is rounded from its exact decimal, so a value just under a half
// rounds down however its nearest double falls, and a half rounds away from zero.
func TestFormatQuantityRoundsTheExactDecimal(t *testing.T) {
	for in, want := range map[string]string{
		"235.5349999999999998608": "235.53",
		"1075.17499999999999985":  "1075.17",
		"0.125":                   "0.13",
		"100":                     "100.00",
		"-0.005":                  "-0.01",
		"not a number":            "not a number",
	} {
		if got := formatQuantity(in); got != want {
			t.Errorf("formatQuantity(%q) = %q, want %q", in, got, want)
		}
	}
}
