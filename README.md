# nn

A config file for [nono](https://nono.sh), so you stop typing this:

```sh
fnox exec -- nono run --profile .nono/profile.json --allow-cwd --no-diagnostics --trust-proxy-ca -- claude
```

and type `nn` instead.

## What it does, and what it deliberately doesn't

Most sandbox policy belongs in nono's `profile.json`, and **nn does not duplicate
any of it**. Filesystem grants, network policy, credentials, ports — all of that
stays in the profile. Put one of those keys in `nn.yml` and nn will tell you
where in the profile it lives instead.

nn exists for the two things a profile cannot reach:

1. **Flags with no profile equivalent** — `--allow-cwd` (the profile sets the
   access *level*, but only the flag skips the prompt), `--no-diagnostics`,
   `--trust-proxy-ca`, `--detached`, `--skip-dir`, the rollback and audit
   family, and the `--allow-unix-socket-*` variants. 47 in total.
2. **The outer wrapper** — `fnox exec --` runs *above* nono, so nono has no
   concept of it.

nn reads `.nono/nn.yml`, assembles the command line, and `exec`s it. It then
disappears from the process tree.

## Install

```sh
go install github.com/jderuiter/nn/cmd/nn@latest
nn init          # writes a commented .nono/nn.yml
nn doctor        # checks the config and every binary in the chain
```

From a clone, `mise run install` puts it in `~/.local/bin` (override with
`PREFIX`).

## Configuration

```yaml
# .nono/nn.yml
profile: profile.json        # relative to .nono/, or a profile name
command: [claude]            # [] to let the profile's own binary: decide
allow_cwd: true

wrappers:
  - [fnox, exec, --]         # each entry carries its own separator

env:
  NONO_THEME: minimal
env_unset:
  - AWS_PROFILE

run:                         # nn      (also nn run)
  trust_proxy_ca: true
  no_diagnostics: true
  skip_dir: [node_modules]

shell:                       # nn shell — takes no command
  shell_bin: /bin/zsh

wrap:                        # nn wrap — for scripts and CI
  command: [go, test, ./...]
```

nn searches upward from the working directory for `.nono/nn.yml`, so it works
from any subdirectory.

### Keys mirror nono's flags

`--no-diagnostics` is `no_diagnostics`, `--trust-proxy-ca` is `trust_proxy_ca`.
`nono run --help` is the reference. Positive-form rewriting would be ambiguous,
because nono ships both `--rollback` and `--no-rollback`.

The one exception is `shell_bin`, which carries `--shell`: the key `shell` names
the shell-mode block.

### Modes are subcommands, and they are not interchangeable

`nono run` exposes 69 flags, `shell` 52, `wrap` 36. `wrap` is direct mode with no
supervisor, so it has no proxy, rollback or audit flags at all. nn owns that
matrix so a mismatch is nn's error, not a clap error from two processes down.

The top level and the per-mode blocks are validated differently, and the
asymmetry is the point:

| Where | A key the mode doesn't accept |
|---|---|
| in a `run:`/`shell:`/`wrap:` block | **hard error** — naming the block asserts it belongs there, so a mismatch is a typo |
| at the top level | **ignored, and reported** — you meant it generally |

That is what lets one file serve both `nn` and `nn wrap` without editing.
Validation covers all three blocks whichever subcommand you run, so a typo in a
block you rarely use surfaces now rather than in CI.

### Environment

`env:` sets variables on the **outer** chain — the wrappers and nono itself.
Whether one reaches the sandboxed child is your profile's decision, via
`environment.allow_vars` / `deny_vars`. `NONO_*` and `PATH` never reach the
child; nono manages those, which is exactly why they belong here — profile
`set_vars` rejects both at load time.

For variables meant purely for the child, use `environment.set_vars` in
`profile.json`.

Values expand `${VAR}` against **your shell environment only**, never against
other `env:` entries — so `PATH: "${HOME}/bin:${PATH}"` means the inherited
PATH, and the block's ordering can never matter. An unknown variable is an
error, not a silent empty string. Use `$$` for a literal `$`.

Under [herdr](https://herdr.dev), nn sets `HERDR_AGENT` from the command it is
about to run, correcting the value herdr guessed from seeing `nn`. Opt out with
`herdr: false`.

## Commands

| | |
|---|---|
| `nn [-- args]` | same as `nn run`; `-- args` replace `command:`, `--append` extends it |
| `nn run` / `nn shell` / `nn wrap` | pick the mode |
| `nn print` | show the invocation without running it (`--format shell\|lines\|json`) |
| `nn init` | write a commented starter config |
| `nn doctor` | check the config, every binary in the chain, and the profile |

Everything after `--` is passed through untouched, so `nn -- go test -v` never
lets nn's own parser see `-v`.

## Why `exec`

nn replaces itself with the chain rather than supervising it. A parent that only
relays would have to forward six signals correctly — and Ctrl-C goes to the whole
foreground process group, so nn could race nono and orphan the sandbox. Exec'ing
also gives exact TTY inheritance and makes the exit code the child's by
construction. It is what `fnox exec`, `mise exec` and `nono wrap` all do.

## Exit codes

`2` for a usage or config error (never `1`, so it cannot be confused with a
child's), `127` when a binary in the chain is missing. Otherwise nn's exit status
is the child's.

## Development

Tasks live in `mise.toml`:

```sh
mise run build       # -> bin/nn
mise run test
mise run vet         # vet, gofmt check, cross-compile to windows and linux
mise run check       # vet + test, what CI would run
mise run repro       # verify the build is byte-for-byte reproducible
mise run install     # PREFIX=~/.local by default
```

Builds are reproducible: `-trimpath` strips local filesystem paths, and the
stamped date is the *source* date — `SOURCE_DATE_EPOCH` if set, otherwise the
commit time — never the wall clock. `mise run repro` builds twice and compares.
`nn version` falls back to the VCS stamps the Go toolchain embeds, so a
`go install` build still identifies itself.

`mise.toml` also pins Go and redirects `GOPATH` in-tree, because `~/go` is not
writable under the nono profile this repo develops against.

The per-mode flag matrix is generated from `nono --help` rather than transcribed,
and a test re-derives it from the installed binary so a nono upgrade that moves a
flag fails loudly. `syscall.Exec` cannot be tested in-process, so the exec path
is covered by subprocess tests against stand-in binaries.
