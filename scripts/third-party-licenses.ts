#!/usr/bin/env -S deno run --allow-read --allow-write --allow-run --allow-env
// third-party-licenses — reproduce, with the binary, the notices its own
// licences require.
//
// fpcloud is a static binary with ~100 third-party modules compiled into it,
// and every licence in that set — MIT, BSD, ISC, Apache-2.0 — grants the right
// to redistribute on the condition that its copyright notice travels along.
// Shipping the binary without them is the one licence term this repo was
// actually in breach of.
//
// The set is derived from `go list -deps` over the BINARY's own import graph,
// not from go.mod: a module required for a test or a tool is not in what we
// hand anybody, and listing it would claim a distribution that never happened.
//
// GOWORK is off for the same reason it is off in CI — inside the workspace tree
// Go resolves one version of a shared dependency across every module, so the
// versions attributed here would be the ones this checkout builds rather than
// the ones a release does.
//
//   just licenses         # regenerate
//   just licenses-check   # regenerate and diff instead of writing
//
// The check is what CI runs, so a dependency added without regenerating fails
// the build rather than shipping unattributed.

// Beside the command that prints it, because that is how it reaches anybody:
// the file is embedded in the binary, and Homebrew installs only the binary
// while Nix builds from source — a release asset would reach neither.
const OUT = "pkg/cli/THIRD_PARTY_LICENSES.md";

// The binary this attributes. Anything reachable only from a test or a tool is
// not distributed and does not belong here.
const TARGET = "./cmd/fpcloud";

const PRODUCT = "fpcloud";

type Mod = { path: string; version: string; dir: string };

const run = async (cmd: string, args: string[]) => {
  const out = await new Deno.Command(cmd, {
    args,
    env: { GOWORK: "off" },
    stdout: "piped",
    stderr: "piped",
  }).output();
  if (!out.success) {
    throw new Error(
      `${cmd} ${args.join(" ")} failed:\n${new TextDecoder().decode(out.stderr)}`,
    );
  }
  return new TextDecoder().decode(out.stdout);
};

const modules = async (): Promise<Mod[]> => {
  const raw = await run("go", [
    "list",
    "-deps",
    "-f",
    "{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}",
    TARGET,
  ]);
  const seen = new Map<string, Mod>();
  for (const line of raw.split("\n")) {
    const [path, version, dir] = line.split("|");
    if (!path || !dir) continue;
    // The main module is what we are licensing, not a third party.
    if (!version) continue;
    seen.set(path, { path, version, dir });
  }
  return [...seen.values()].sort((a, b) => a.path.localeCompare(b.path));
};

// A licence lives at the module root. Deeper copies belong to vendored code we
// do not compile, so reaching for them would attribute something absent from
// the binary.
const fileNamed = (dir: string, names: RegExp) => {
  const hits: string[] = [];
  for (const e of Deno.readDirSync(dir)) {
    if (e.isFile && names.test(e.name)) hits.push(e.name);
  }
  return hits.sort();
};

const LICENSE_RE = /^(LICEN[SC]E|COPYING)(\.(md|txt))?$/i;
const NOTICE_RE = /^NOTICE(\.(md|txt))?$/i;

// Named from the text rather than from a manifest, because the text is the
// licence and a manifest is a claim about it. Only ever a label for the index —
// the reproduced text below it is what carries the terms.
const nameOf = (text: string) => {
  const head = text.slice(0, 4000);
  // The GNU family is matched on its title ALONE ON A LINE, and before nothing.
  // MPL-2.0 names "the GNU General Public License" in its Secondary License
  // definition, so a substring test reports every hashicorp module as GPL —
  // which is how the first draft of this file described terraform-plugin-go.
  if (/^[ \t]*GNU AFFERO GENERAL PUBLIC LICENSE[ \t]*$/im.test(head)) {
    return "AGPL-3.0";
  }
  if (/^[ \t]*GNU LESSER GENERAL PUBLIC LICENSE[ \t]*$/im.test(head)) {
    return "LGPL";
  }
  if (/^[ \t]*GNU GENERAL PUBLIC LICENSE[ \t]*$/im.test(head)) return "GPL";
  if (/Mozilla Public License/i.test(head)) return "MPL-2.0";
  if (/Business Source License/i.test(head)) return "BUSL-1.1";
  if (/Apache License/i.test(head)) return "Apache-2.0";
  if (/Redistribution and use in source and binary forms/i.test(head)) {
    return /neither the name/i.test(head) ? "BSD-3-Clause" : "BSD-2-Clause";
  }
  if (/Permission is hereby granted, free of charge/i.test(head)) return "MIT";
  if (/Permission to use, copy, modify, and\/or distribute/i.test(head)) {
    return "ISC";
  }
  return "see text";
};

const sha256 = async (s: string) => {
  const buf = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(s),
  );
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0"))
    .join("");
};

const render = async (mods: Mod[]) => {
  type Entry = { mod: Mod; name: string; text: string; notices: string[] };
  const entries: Entry[] = [];
  const missing: string[] = [];

  for (const mod of mods) {
    const files = fileNamed(mod.dir, LICENSE_RE);
    if (files.length === 0) {
      missing.push(`${mod.path}@${mod.version}`);
      continue;
    }
    const text = files
      .map((f) => Deno.readTextFileSync(`${mod.dir}/${f}`).trimEnd())
      .join("\n\n");
    // Apache-2.0 §4(d): a NOTICE that ships with the work has to ship with
    // ours. Never deduplicated — a NOTICE names its own author.
    const notices = fileNamed(mod.dir, NOTICE_RE)
      .map((f) => Deno.readTextFileSync(`${mod.dir}/${f}`).trimEnd());
    entries.push({ mod, name: nameOf(text), text, notices });
  }

  if (missing.length > 0) {
    throw new Error(
      `no licence file in the module root of:\n  ${missing.join("\n  ")}\n` +
        `Every module compiled in has to be attributable. Find its terms and ` +
        `add a case here rather than dropping it from the file.`,
    );
  }

  // Grouped by the exact text, so the Apache-2.0 text appears once rather than
  // sixty-seven times. Safe because a licence carrying its holder's name — MIT,
  // BSD, ISC — differs per module and so never collides; what collides is the
  // boilerplate that names nobody.
  const groups = new Map<string, { name: string; text: string; mods: Mod[] }>();
  for (const e of entries) {
    const key = await sha256(e.text);
    const g = groups.get(key) ?? { name: e.name, text: e.text, mods: [] };
    g.mods.push(e.mod);
    groups.set(key, g);
  }
  const ordered = [...groups.values()].sort((a, b) =>
    a.mods[0].path.localeCompare(b.mods[0].path)
  );

  const out: string[] = [];
  out.push(`# Third-party licenses`);
  out.push("");
  out.push(
    `${PRODUCT} is distributed as a single binary with the modules below ` +
      `compiled into it. Each one is reproduced here under the terms it is ` +
      `offered on; the licence text is authoritative and the name beside a ` +
      `module is a label for reading, not a term.`,
  );
  out.push("");
  out.push(
    `Generated by \`scripts/third-party-licenses.ts\` from the import graph of ` +
      `\`${TARGET}\`. Do not edit — CI regenerates this file and fails on a diff.`,
  );
  out.push("");
  out.push(`## Modules`);
  out.push("");
  for (const e of entries) {
    out.push(`- ${e.mod.path} ${e.mod.version} — ${e.name}`);
  }
  out.push("");

  const withNotices = entries.filter((e) => e.notices.length > 0);
  if (withNotices.length > 0) {
    out.push(`## Notices`);
    out.push("");
    for (const e of withNotices) {
      out.push(`### ${e.mod.path}`);
      out.push("");
      out.push("```");
      out.push(...e.notices);
      out.push("```");
      out.push("");
    }
  }

  out.push(`## License texts`);
  out.push("");
  for (const g of ordered) {
    out.push(`### ${g.name}`);
    out.push("");
    for (const m of g.mods) out.push(`- ${m.path}`);
    out.push("");
    out.push("```");
    out.push(g.text);
    out.push("```");
    out.push("");
  }
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trimEnd() + "\n";
};

const check = Deno.args.includes("--check");
const wanted = await render(await modules());

if (!check) {
  await Deno.writeTextFile(OUT, wanted);
  console.log(`wrote ${OUT}`);
  Deno.exit(0);
}

const have = await Deno.readTextFile(OUT).catch(() => "");
if (have === wanted) {
  console.log(`${OUT} is up to date`);
  Deno.exit(0);
}
console.error(
  `${OUT} is stale — a module was added, removed or moved without ` +
    `regenerating it.\n\n  deno run --allow-read --allow-write --allow-run ` +
    `--allow-env scripts/third-party-licenses.ts\n`,
);
Deno.exit(1);
