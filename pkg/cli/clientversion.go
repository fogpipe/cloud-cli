package cli

import (
	"regexp"

	"golang.org/x/mod/semver"
)

// describeSuffix is what `git describe --tags` appends past a tag: the commit
// count and the abbreviated hash, and `-dirty` for a tree with changes.
var describeSuffix = regexp.MustCompile(`(-\d+-g[0-9a-f]+)?(-dirty)?$`)

// clientVersionBelow reports whether this build is older than the floor a
// deployment states. A build the workspace stamps from git reads
// `v0.242.0-1-gd522208`: one commit past the tag. semver reads that suffix as a
// prerelease and orders it before v0.242.0, which is how `fpcloud version`
// went on telling a build the deployment admits that it was refused, after
// the server had learned to read the suffix as "past the tag". The build is
// judged by the tag it descends from; a real prerelease (`v1.0.0-rc.1`)
// carries no describe suffix and is compared as written.
func clientVersionBelow(version, floor string) bool {
	tag := describeSuffix.ReplaceAllString(version, "")
	if !semver.IsValid(tag) || !semver.IsValid(floor) {
		return false
	}
	return semver.Compare(tag, floor) < 0
}
