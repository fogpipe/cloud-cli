package cli

import (
	"strings"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestRunningImage(t *testing.T) {
	for _, c := range []struct {
		name string
		app  client.AppStatus
		want string
	}{
		{"a ready pod's image", client.AppStatus{Ready: 1, Image: "img:2", RunningImage: "img:1"}, "img:1"},
		{"ready pods that could not be read", client.AppStatus{Ready: 1, Image: "img:2"}, notRead},
		{"nothing ready falls back to what was asked for", client.AppStatus{Image: "img:2"}, "img:2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := runningImage(c.app); got != c.want {
				t.Errorf("runningImage = %q, want %q", got, c.want)
			}
		})
	}
}

func TestReleaseLabel_ReadyPodsThatCouldNotBeRead(t *testing.T) {
	if got := releaseLabel(client.AppStatus{Ready: 1, Release: "v2"}); !strings.Contains(got, notRead) {
		t.Errorf("releaseLabel = %q, want it to say %q", got, notRead)
	}
	if got := releaseLabel(client.AppStatus{Release: "v2"}); got != "v2" {
		t.Errorf("releaseLabel = %q, want the release asked for when nothing is ready", got)
	}
	// A pod with no release label is a pod that was read.
	if got := releaseLabel(client.AppStatus{Ready: 1, RunningImage: "img:1", Release: "v2"}); got != "v2" {
		t.Errorf("releaseLabel = %q, want the release when the pod was read and carries none", got)
	}
}
