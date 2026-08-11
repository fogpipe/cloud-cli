package cli

import "testing"

// The app is never implicit, so `rollback web` has to mean "roll app web back to
// the previous release" — not "roll the --app-less command back to a release
// called web" (#471).
func TestSplitRollbackArgs(t *testing.T) {
	tests := []struct {
		name    string
		appFlag string
		args    []string
		wantApp string
		wantRel string
	}{
		{name: "app and release positional", args: []string{"web", "v1.0.0"}, wantApp: "web", wantRel: "v1.0.0"},
		{name: "app only defaults to previous", args: []string{"web"}, wantApp: "web"},
		{name: "release with --app", appFlag: "web", args: []string{"v1.0.0"}, wantRel: "v1.0.0"},
		{name: "--app alone defaults to previous", appFlag: "web"},
		{name: "nothing given", wantApp: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newAppRefCmd()
			if tt.appFlag != "" {
				if err := c.Flags().Set("app", tt.appFlag); err != nil {
					t.Fatal(err)
				}
			}
			appArgs, release := splitRollbackArgs(c, tt.args)
			got := ""
			if len(appArgs) > 0 {
				got = appArgs[0]
			}
			if got != tt.wantApp {
				t.Errorf("app arg = %q, want %q", got, tt.wantApp)
			}
			if release != tt.wantRel {
				t.Errorf("release = %q, want %q", release, tt.wantRel)
			}
		})
	}
}
