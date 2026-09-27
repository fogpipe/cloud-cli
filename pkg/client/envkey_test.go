package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvAPIKey(t *testing.T) {
	file := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(file, []byte("fp-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, key, file, want string
		wantErr               bool
	}{
		{name: "nothing set"},
		{name: "variable", key: "fp-var", want: "fp-var"},
		{name: "file", file: file, want: "fp-from-file"},
		{name: "variable outranks file", key: "fp-var", file: file, want: "fp-var"},
		{name: "unreadable file", file: filepath.Join(t.TempDir(), "missing"), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("FPCLOUD_API_KEY", c.key)
			t.Setenv("FPCLOUD_API_KEY_FILE", c.file)
			got, err := EnvAPIKey()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
