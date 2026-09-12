# CLAUDE.md

Architecture and design decisions for `nn`. User-facing docs are in
[README.md](README.md).

## What this is

A thin arg-builder. nn reads `nn.yml`, assembles a command line for
[nono](https://nono.sh), and `exec`s it. The core is ~200 lines of slice
concatenation; almost everything else is validation and error messages.

nn deliberately carries **only** what a nono profile cannot express:

1. The 47 nono flags with no profile equivalent.
2. The outer wrapper command (`fnox exec --`), which runs above nono entirely.

Anything settable in `profile.json` — filesystem grants, network policy,
credentials, ports — has no key here and is rejected as unknown, with an error
naming where in the profile it belongs. Resist requests to add such keys.

## Layout

```
cmd/nn/main.go           dispatch, ldflags vars
internal/config/         schema, tri-state types, spec table, merge, validate
internal/discover/       config search, path/name resolution
internal/nono/           argv assembly, env building
internal/herdr/          HERDR_AGENT inference
internal/runner/         syscall.Exec, PATH-aware LookPath
internal/cli/            flags, subcommands, print/init/doctor
internal/testbin/        stand-in nono and wrapper, for exec tests
```

`internal/runner` rather than `internal/exec`, to avoid shadowing `os/exec`.

## Design decisions

### The spec table is the single source of truth

`internal/config/spec.go` holds one ordered `[]FlagSpec`. Emission, mode
validation, `-c` exclusivity, path anchoring and `doctor` all derive from it.
Adding a nono flag means adding one row.

Two tests guard it:

- `TestSpecCoverage` asserts every `Section` field is in exactly one spec or on
  the nn-only allowlist. Without it, adding a field and forgetting to emit it is
  silent.
- `TestModeMatrixMatchesInstalledNono` re-derives the matrix from
  `nono {run,shell,wrap} --help` and fails on drift. The matrix was generated
  from that output originally, not transcribed, and a nono upgrade that moves a
  flag between modes must fail loudly rather than surface as a clap error two
  processes down.

### Modes are subcommands, not a config key

`nono run` exposes 69 flags, `shell` 52, `wrap` 36 — `wrap` is direct mode with
no supervisor, so no proxy, rollback or audit flags. They differ enough to be
distinct verbs. A `mode:` key gets a dedicated error pointing at the
subcommands.

### Block keys are strict, top-level keys are lenient

| Where | A key the mode doesn't accept |
|---|---|
| in a `run:`/`shell:`/`wrap:` block | hard error — naming the block asserts it belongs there, so a mismatch is a typo |
| at the top level | dropped and reported — you meant it generally |

This asymmetry is what lets one file serve both `nn` and `nn wrap` without
editing. Validation walks all three blocks whichever subcommand runs, so a typo
in a rarely-used block surfaces immediately.

Merging is replace, not append — unlike nono's profile `extends`. nono's arrays
are capability grants where appending is the safe direction; nn's are argv
fragments, where `wrap: {command: [go, test]}` must mean "instead of". `env`
merges per key and `env_unset` unions, because both are sets.

### Keys mirror nono's flag names

`--no-diagnostics` is `no_diagnostics`, not `diagnostics: false`. nono ships
*both* `--rollback` and `--no-rollback` (and both halves of
`--audit-integrity`), so a positive tri-state cannot express which you meant.
Mirroring also makes `nono run --help` the reference doc.

The one exception is `shell_bin` for `--shell`, since `shell` names the
shell-mode block. Disambiguating `shell:` by value type would produce
incomprehensible errors.

### Tri-state values

`Bool`, `Int`, `Count`, `Str` in `internal/config/tristate.go` distinguish unset
from an explicit zero. Needed in two places: overlaying CLI flags onto YAML (an
unset flag must not clobber a config value), and flags where zero is meaningful
— `--startup-timeout 0` disables the check.

Each implements both `yaml.BytesUnmarshaler` and `flag.Value`, which makes a CLI
override one line. Preferred over `*bool`/`*int`: pointers aren't comparable in
table-test literals and invite nil derefs in the emit loop.

### Discovery is two directories, not a walk

`discover.Find` checks the cwd and, when inside a git repository, the repo root.
In each it accepts a bare `nn.yml` or one under `.nono/`, either spelling. Eight
candidates, first match wins, ordered location-major: `./nn.yaml` outranks
`./.nono/nn.yml`, because the question "which directory owns this project" is
the one the user is actually answering.

It used to walk from the cwd to `/`. That is an unbounded search over paths the
user never named: a `.nono/nn.yml` in `$HOME`, or in a parent checkout that
happens to contain this one, silently claims an unrelated project, and the
symptom is a sandbox policy that came from nowhere visible. The git root is the
one boundary above the cwd that reliably means *this project*, and it is also a
ceiling — a config above an inner repo belongs to the outer one.

The cost is that `$HOME/.nono/nn.yml` stops working as a personal default.
That is the intended trade: `--config`/`$NN_CONFIG` covers it explicitly.

`gitRoot` probes for a `.git` entry rather than running
`git rev-parse --show-toplevel`. nn runs inside the sandboxes it builds, where
git is not guaranteed to be on PATH or permitted to execute, and a failure there
would surface as "no nn.yml found". Existence is the test, not `IsDir` — a
worktree or submodule has `.git` as a file holding a `gitdir:` pointer.

Two spellings in the *same* directory stays a hard error while ordering across
directories is a precedence rule. The asymmetry is deliberate: a search order is
something the user can reason about and exploit, whereas a winner inside one
directory would just make edits to the loser do nothing.

### Paths are absolutised, with two anchors

nono runs from the user's working directory, which may be far below the project
root, so every path nn emits is made absolute first.

- `profile` and `config` anchor to the config's own directory, so
  `profile: profile.json` names the file sitting next to `nn.yml` in either
  layout. This is also what `--config` has always done, so there is one rule
  rather than a special case.
- Everything else path-shaped anchors to the project root — `workdir: .` meaning
  the root is the intuitive reading.

The root is the config's directory, except under `.nono/`, where it is the
parent. It cannot be the grandparent as it once was: for a bare `nn.yml` that
puts the `workdir`, `log_file` and socket anchors *outside* the project, and for
`--config /tmp/x.yml` it made the root `/`.

`$VAR` is deliberately **not** expanded in paths. nono profiles expand
`$HOME`/`$WORKDIR` with their own semantics; a second layer would mean the same
string means different things in adjacent files.

A profile value may be a name rather than a path. A slash alone is not enough to
decide — `nolabs-ai/claude` is a registry pack. It is a path only if explicitly
rooted, carrying a profile extension, or naming a file that exists under
`.nono/`. This matches nono, which reports "profile file not found" for
`profile.json`.

### syscall.Exec, not supervision

nn has no work left once argv is assembled. As a parent it would have to forward
SIGINT/TERM/QUIT/HUP/WINCH/TSTP correctly — and Ctrl-C goes to the whole
foreground process group, so nn would receive it alongside nono and could race
to exit first, orphaning the sandbox. Exec'ing gives exact TTY inheritance and
makes the exit code the child's by construction. It is what `fnox exec`,
`mise exec` and `nono wrap` all do.

`exec_other.go` (`//go:build !unix`) exists only to keep non-Unix builds and
vet green.

### LookPath resolves against the built PATH

`exec.LookPath` reads nn's *own* PATH. With `env: {PATH: ...}` in the config,
that would find one `nono` and hand control to an environment where a different
one is first — near-undiagnosable from outside. `runner.LookPathIn` resolves
`argv[0]` against the PATH nn is about to exec with.
`TestPathOverrideAffectsResolution` is the regression test; it fails against a
naive `exec.LookPath`.

### Environment

nn never *translates* a typed key into a `NONO_*` var — it always emits flags.
One emitter, one format, one thing `nn print` shows; and clap's env fallback
only applies when the flag is absent, so flags win deterministically.

`env:` is a separate, user-declared passthrough. It is not merely convenient:
profile `set_vars` reserves `PATH` and `NONO_*`, so those are **not expressible
in a profile at all**.

Expansion reads the host environment only, never other `env:` entries. That
makes map iteration order provably irrelevant and makes
`PATH: "${HOME}/bin:${PATH}"` mean the inherited PATH. An unknown variable is an
error — `os.ExpandEnv` blanking unknown names is how you get `PATH=":/usr/bin"`.
nono's own `$WORKDIR`/`$NONO_CONFIG` tokens get a targeted message.

Removal is `env_unset:`, not `env: {FOO: null}`. YAML parses a bare `FOO:` as
null, so the null design would make that typo silently mean "remove" rather than
"set empty" — and `FOO=` versus absent are genuinely different in Unix.

Overrides replace in place rather than appending, so `nn print` shows one
`PATH=` and two prints differ only where the config did.

### Exit codes

**2** for usage and config errors — deliberately not 1, so nn's own failures
never collide with a child's. **127** when a binary in the chain is missing.
After a successful exec, nn's status is the child's.

Error messages follow one shape: what failed, where, how to fix it. Since nn
exists to collapse a four-binary chain, an error that doesn't name *which link*
broke would undo the value.

## Testing

The core is `Config -> []string`, a pure function, covered by table tests in
`internal/nono`. `TestModeMatrixEnforcement` generates ~90 assertions by
iterating the spec table.

`syscall.Exec` replaces the process, so the exec path is structurally
untestable in-process. `internal/cli/integration_test.go` builds `nn` plus
stand-in binaries and runs them as subprocesses, from a *subdirectory* of a
fixture carrying a `.git` marker, so git-root discovery is exercised too. That
is the only proof that exec's third argument is wired correctly.

When adding a verification task or check, confirm it fails when it should.
Two bugs of exactly that shape have already appeared here: `gofmt -l` exits 0
even when it finds problems, and `mise run build` inside the repro task was
skipped by source caching, so it compared two files that were never written.

## Build

```sh
mise run build      # -> bin/nn
mise run check      # vet, gofmt, cross-compile, test
mise run repro      # build twice, compare
mise run install    # PREFIX=~/.local
```

`mise run -f build` to force (flags go before the task name; after it they are
passed to the task).

Builds are reproducible: `-trimpath` strips local paths, and the stamped date is
the source date — `SOURCE_DATE_EPOCH`, else the commit time — never the wall
clock. `nn version` falls back to the toolchain's embedded VCS stamps so a
`go install` build still identifies itself.

`mise.toml` pins Go and redirects `GOPATH` in-tree: `~/go` is not writable under
the nono profile this repo develops against. For the same reason Go is pinned to
an already-installed version rather than the newest — `~/.local/share/mise/installs`
is read-only in that sandbox.

Dependencies: one, `goccy/go-yaml`, chosen for error quality — it annotates with
line, column and a source excerpt, which is most of the config UX.
