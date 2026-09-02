# fpcloud CLI

The Fogpipe Cloud CLI and its Go SDK. Public, Apache-2.0.

## What this repo is

- `pkg/cli/` — the whole command tree (Cobra + Charm UI). `NewRootCommand()` is
  exported so the platform repo can build the tree and validate its docs pool
  against the real cobra registration.
- `cmd/fpcloud/` — a thin `main` over `pkg/cli`. Keep it thin; anything with
  logic in it is unreachable from that check.
- `pkg/client/` — stdlib-only Go SDK for the REST API. Imported by the platform
  server, the OpenTofu provider and this CLI, so it is a published interface,
  not an internal helper.

## It is public

Nothing operator-internal goes here — no command, and no `pkg/client` method for
a route only an operator may call. Operator capability lives in `fpadmin`, built
from `cloud-platform` and released to nobody (ADR-089).

The rule takes no exception, in either direction. What made
`fke get-credentials --staff` wrong was that it *hid* an operator capability
inside a tenant command, and a hidden flag is not a boundary. A *visible* group
a tenant is refused with 403 is no better: the boundary holds, and the surface,
its flags and its client methods still ship to every machine that installs this
binary.

Two `pkg/client` methods still reach an operator route — `UpdateOrgFKE` and
`UpdateProjectQuota`, which the Terraform provider's `fke_enabled` and quota
attributes call. They are the open end of this rule, not a precedent
(`fogpipe/cloud-workspace#121`); nothing new joins them.

No credential belongs in this binary, and none is in it. The Google client
secret the token exchange needs lives on the platform, which brokers the
exchange at `/api/v1/auth/oauth/token`; `oidcClientSecret` is only ever set for
a non-Google client, from the environment. Don't reintroduce a build-time
injection — it is what stopped this from being buildable from source.

Read rather than contributed to: there is no contribution process, and issues
here are not a support channel.

## What the client bounds, and what it retries

`pkg/client` sets **per-phase** bounds only — dial, TLS handshake, response
header — and never a total deadline. A total deadline covers reading the body,
so it cannot tell a long answer from a stuck server, and it cut `app logs
--follow` at 30 seconds where ADR-086 promises thirty minutes. The caller's
context ends anything longer, and is the real bound (ADR-107).

It **retries a 429** on its own, honouring `Retry-After` with added jitter,
bounded in attempts, and never past the caller's context (ADR-115). This is safe
for a `POST`, which it would not ordinarily be: the platform refuses a
rate-limited request before it reaches the handler, so no work happened and
there is no half-created resource for a repeat to collide with. The jitter is
added, never subtracted — one org's clients share one budget, so they are
refused together and told the same second to return.

Nothing else is retried. A 5xx may have done work.

## A change here is a release

The platform and the provider both depend on `github.com/fogpipe/cloud-cli` at a
tagged version. Nothing downstream can move until a change is merged **and
tagged** — a merged-but-untagged client change is invisible to both, and reads
as the method not existing rather than as a missing tag.

Pushing a `v*` tag builds the binaries, publishes the release and bumps the
version in the Homebrew formula and the Nix package. No secrets are needed for
a release, so a tag is all it takes.

The tag is also what a deployment judges a caller by. `pkg/client` states its
version on every request (`X-Fpcloud-Client-Version`), read from the caller's
build info — so a consumer states it by depending on the module, and the CLI,
which is this module's own binary and has no such entry, sets its ldflags value
instead. If a change here **breaks the wire**, the platform raises its minimum
to this tag and every older client is refused with 426 rather than failing on
whatever the request happens to hit (ADR-073).

**The tag is not cut here.** This repo, the platform and the provider carry one
shared version, pushed to all three at once by `just release vX.Y.Z` at the
workspace root (`/release-semver`). Tagging this repo alone is what pulled the
three numbers apart before — it releases fastest, so it runs ahead and the
number stops meaning the same thing anywhere else.

To move a merged client change downstream without releasing everything,
consumers can pin its commit — `go get github.com/fogpipe/cloud-cli@<commit>`
resolves a pseudo-version with no tag involved.

## A dependency is attributed before it ships

The binary embeds `pkg/cli/THIRD_PARTY_LICENSES.md` and prints it with
`fpcloud licenses`. That is not a courtesy — MIT, BSD, ISC and Apache-2.0 all
grant redistribution *on the condition* that their copyright notice travels
with the binary, and this is a binary we hand out through GitHub releases,
Homebrew and Nix.

- **Regenerate after any dependency change**: `just licenses`. CI regenerates
  and diffs, so a module added without attributing it fails the build rather
  than shipping unattributed.
- **Embedded, not a release asset.** Homebrew installs only the binary and Nix
  builds from source, so an asset beside the release would reach neither — and
  a build from a tag has to be as compliant as the one CI publishes.
- The set comes from `go list -deps` over `./cmd/fpcloud`, so a module reached
  only by a test or a tool is correctly absent: it is not in what anyone
  receives.

## Working across repos

`just tree <name>` at the workspace root gives a session an isolated copy of
the whole workspace — this repo and the platform repo under one `go.work` — so
a `pkg/client` change is testable against the server before it is tagged.
Without it you are testing against the last tag.

## Docs live in the platform repo

`fpcloud docs` fetches `/docs/index.json`, `/docs/md/<topic>.md` and
`/llms-full.txt` from the platform host. The pool is not embedded here — a guide
corrected today reads correctly from a binary installed months ago. `--help-llm`
is built from the cobra tree and needs no network.

## Conventions

- `go build ./...`, `go test ./...`, `gofmt` — CI gates all three.
- Dependency versions track the platform's. A fresh `go mod tidy` resolves to
  latest and would drag the server's graph forward through this module; pin to
  what the platform pins unless the upgrade is the point.
