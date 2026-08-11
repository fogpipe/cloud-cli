package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseObjectURL(t *testing.T) {
	cases := []struct {
		in         string
		wantBucket string
		wantKey    string
		wantRemote bool
	}{
		{"fps://mybucket/path/to/key", "mybucket", "path/to/key", true},
		{"s3://mybucket/path/to/key", "mybucket", "path/to/key", true},
		{"fps://mybucket", "mybucket", "", true},
		{"fps://mybucket/", "mybucket", "", true},
		{"s3://b/a/b/c.txt", "b", "a/b/c.txt", true},
		{"./local/path", "", "", false},
		{"/abs/path", "", "", false},
		{"-", "", "", false},
		{"plainname", "", "", false},
		{"fps://", "", "", true},
	}
	for _, c := range cases {
		b, k, r := parseObjectURL(c.in)
		if b != c.wantBucket || k != c.wantKey || r != c.wantRemote {
			t.Errorf("parseObjectURL(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, b, k, r, c.wantBucket, c.wantKey, c.wantRemote)
		}
	}
}

func TestIncludedByFilters(t *testing.T) {
	cases := []struct {
		name  string
		rules []filterRule
		path  string
		want  bool
	}{
		{"no rules includes everything", nil, "any/file.txt", true},
		{"exclude matches", []filterRule{{false, "*.log"}}, "app.log", false},
		{"exclude non-match stays included", []filterRule{{false, "*.log"}}, "app.txt", true},
		{
			"exclude-all then re-include txt: txt wins",
			[]filterRule{{false, "*"}, {true, "*.txt"}},
			"notes.txt", true,
		},
		{
			"exclude-all then re-include txt: log excluded",
			[]filterRule{{false, "*"}, {true, "*.txt"}},
			"app.log", false,
		},
		{
			"later rule wins (include then exclude)",
			[]filterRule{{true, "*.txt"}, {false, "secret.txt"}},
			"secret.txt", false,
		},
		{
			"basename match on nested key",
			[]filterRule{{false, "*.log"}},
			"deep/nested/app.log", false,
		},
	}
	for _, c := range cases {
		if got := includedByFilters(c.rules, c.path); got != c.want {
			t.Errorf("%s: includedByFilters(%v, %q) = %v, want %v", c.name, c.rules, c.path, got, c.want)
		}
	}
}

func TestContentTypeFor(t *testing.T) {
	cases := []struct{ key, want string }{
		{"index.html", "text/html; charset=utf-8"},
		{"assets/styles.css", "text/css; charset=utf-8"},
		{"app.js", "text/javascript; charset=utf-8"},
		{"mod.mjs", "text/javascript; charset=utf-8"},
		{"logo.svg", "image/svg+xml"},
		{"data.json", "application/json"},
		{"pic.PNG", "image/png"},
		{"photo.jpeg", "image/jpeg"},
		{"font.woff2", "font/woff2"},
		{"bundle.wasm", "application/wasm"},
		{"noext", ""},
		{"archive.unknownext", ""},
	}
	for _, c := range cases {
		if got := contentTypeFor(c.key); got != c.want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestJoinKey(t *testing.T) {
	cases := []struct{ prefix, rel, want string }{
		{"", "a/b.txt", "a/b.txt"},
		{"pre", "a/b.txt", "pre/a/b.txt"},
		{"pre/", "a/b.txt", "pre/a/b.txt"},
		{"pre", "/a/b.txt", "pre/a/b.txt"},
	}
	for _, c := range cases {
		if got := joinKey(c.prefix, c.rel); got != c.want {
			t.Errorf("joinKey(%q,%q) = %q, want %q", c.prefix, c.rel, got, c.want)
		}
	}
}

func TestWebCacheControlFor(t *testing.T) {
	// An entry document must always be revalidated. With versioned deploys the
	// public URL does not change between versions, so a cached index would keep
	// pointing at assets the live version no longer has.
	assert.Equal(t, revalidateCacheControl, webCacheControlFor("index.html"))
	assert.Equal(t, revalidateCacheControl, webCacheControlFor("blog/post.htm"))

	// Fingerprinted assets are safe forever — both conventions in the wild.
	assert.Equal(t, immutableCacheControl, webCacheControlFor("assets/index-DfGh1a2b.js"))
	assert.Equal(t, immutableCacheControl, webCacheControlFor("assets/main.a1b2c3d4.css"))
	assert.Equal(t, immutableCacheControl, webCacheControlFor("_next/static/chunks/framework-8ab12cd9.js"))

	// Everything else takes a short, recoverable TTL.
	assert.Equal(t, defaultCacheControl, webCacheControlFor("logo.png"))
	assert.Equal(t, defaultCacheControl, webCacheControlFor("assets/hero.jpg"))
	assert.Equal(t, defaultCacheControl, webCacheControlFor("robots.txt"))
}

// The dangerous direction is a false positive: a year-long immutable cache on a
// file whose name can be reused cannot be undone from the server side. These are
// all plausible real filenames that must NOT be treated as fingerprinted.
func TestIsFingerprinted_DoesNotMatchOrdinaryNames(t *testing.T) {
	for _, name := range []string{
		"bootstrap.min.js",
		"jquery.slim.js",
		"styles.css",
		"vendor.bundle.js",  // no digits — a word, not a hash
		"app-production.js", // no digits
		"chart.v5.js",       // too short
		"index.html",
		"README", // no extension
	} {
		assert.False(t, isFingerprinted(name), name)
	}

	for _, name := range []string{
		"index-DfGh1a2b.js",
		"main.a1b2c3d4.css",
		"app.0f8e7d6c5b4a.js",
	} {
		assert.True(t, isFingerprinted(name), name)
	}
}

func TestPutOpts_CacheControlPrecedence(t *testing.T) {
	// Without web defaults, a plain transfer sets nothing at all.
	assert.Empty(t, putOpts{}.cacheControlFor("index.html"))

	// An explicit override beats the default, matching on base name or full key.
	o := putOpts{webCache: true, cacheRules: []cacheRule{{pattern: "*.html", value: "public, max-age=60"}}}
	assert.Equal(t, "public, max-age=60", o.cacheControlFor("index.html"))
	assert.Equal(t, immutableCacheControl, o.cacheControlFor("assets/index-DfGh1a2b.js"))

	// First match wins, so an earlier rule shadows a later one.
	o = putOpts{cacheRules: []cacheRule{
		{pattern: "assets/*", value: "first"},
		{pattern: "*.js", value: "second"},
	}}
	assert.Equal(t, "first", o.cacheControlFor("assets/app.js"))
}

func TestParseCacheControlFlags(t *testing.T) {
	rules, err := parseCacheControlFlags([]string{"*.html=no-cache", " *.js = public, max-age=99 "})
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, "*.html", rules[0].pattern)
	assert.Equal(t, "no-cache", rules[0].value)
	// A value containing '=' survives: only the first '=' separates.
	assert.Equal(t, "public, max-age=99", rules[1].value)

	for _, bad := range []string{"nokey", "=value", "pattern=", ""} {
		_, err := parseCacheControlFlags([]string{bad})
		assert.Error(t, err, bad)
	}
}

// TestListMetaMissingSource guards #560: a missing local sync source used to
// return an empty map and no error, so every destination key fell into the
// --delete branch and the bucket was wiped with exit 0. A missing source is a
// user error; a missing local *destination* is still just a dir to create.
func TestListMetaMissingSource(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := listMeta(ctx, nil, false, missing, "", true)
	if err == nil {
		t.Fatal("a missing sync source must be an error, not an empty file set")
	}

	got, err := listMeta(ctx, nil, false, missing, "", false)
	if err != nil {
		t.Fatalf("a missing sync destination must be tolerated: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty destination listing, got %d entries", len(got))
	}
}
