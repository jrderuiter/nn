# nn

Stop typing nono.sh command lines. Start typing `nn`.

```sh
# before
fnox exec -- nono run --profile .nono/profile.json --allow-cwd --no-diagnostics --trust-proxy-ca -- claude

# after
nn
```

nn reads `.nono/nn.yml`, builds that command line, and runs it.

## Install

```sh
go install github.com/jderuiter/nn/cmd/nn@latest
```

Requires [nono](https://nono.sh). From a clone: `mise run install`.

## Quick start

```sh
cd myproject
nn init          # writes .nono/nn.yml
```

Edit it to say which profile to use and what to run:

```yaml
profile: go-dev          # a profile name, or a file in .nono/
command: [go, test, ./...]
allow_cwd: true

run:
  trust_proxy_ca: true
```

Check what it will do, then do it:

```console
$ nn print
nono run --profile go-dev --allow-cwd --trust-proxy-ca -- go test ./...

$ nn
```

`nn` works from any subdirectory — it searches upward for `.nono/nn.yml`.

### Useful from here

```sh
nn -- go build ./...     # run something else, this once
nn --append -- -race     # add args to the configured command
nn shell                 # interactive shell in the sandbox
nn wrap -- go test       # direct mode, for scripts and CI
nn doctor                # check the config and every binary it needs
```

## Configuration

### What goes here, and what doesn't

**Sandbox policy goes in `profile.json`** — filesystem grants, network rules,
credentials, ports. nn does not duplicate any of it.

**`nn.yml` carries what a profile can't express**: nono flags with no profile
equivalent, and the wrapper command that runs *above* nono. If you put a profile
key here by mistake, nn tells you where it belongs:

```console
$ nn print
nn: .nono/nn.yml:
[3:1] unknown field "allow_domain"
>  3 | allow_domain: [proxy.golang.org]
       ^

`--allow-domain` is expressible in the nono profile — set network.allow_domain in your profile.json instead.
```

### Keys

Key names are nono's flag names with `-` as `_`: `--no-diagnostics` is
`no_diagnostics`. So `nono run --help` doubles as the key reference.

The ones you'll actually reach for:

| Key | Type | Does |
|---|---|---|
| `profile` | string | profile name, or a file in `.nono/` |
| `command` | list | what to run; `[]` defers to the profile's `binary` |
| `allow_cwd` | bool | grant the working directory without prompting |
| `wrappers` | list of lists | command prefixes to run before nono |
| `trust_proxy_ca` | bool | trust the proxy CA, so Go tooling works |
| `no_diagnostics` | bool | drop nono's footer on failure |
| `skip_dir` | list | skip big trees during trust scanning |
| `env` / `env_unset` | map / list | set or remove variables |
| `nono_args` | list | escape hatch, passed through verbatim |

Also supported: `extends`, `config`, `workdir`, `bypass_protection`,
`suppress_save_prompt`, the six `allow_unix_socket_*` variants, `proxy_port`,
`proxy_ca_validity`, `strict_broker_path`, `allow_gpu`,
`allow_launch_services`, `capability_elevation`, `name`, `detached`,
`detach_timeout`, `startup_timeout`, `shell_bin`, `memory`, `max_processes`,
the `rollback*` and `audit*` families, `trust_override`, `diagnostics_json`,
`dry_run`, `silent`, `verbose`, `theme`, `log_file`.

One naming exception: `shell_bin` carries `--shell`, because `shell` names the
shell-mode block.

### Per-mode blocks

`run`, `shell` and `wrap` accept different flags — `wrap` is direct mode, so it
has no proxy, rollback or audit flags at all. Put mode-specific settings in a
block:

```yaml
profile: profile.json
allow_cwd: true            # applies everywhere it's supported

run:
  trust_proxy_ca: true     # run and shell only
wrap:
  command: [go, test, ./...]
```

A key in a block that the mode rejects is an **error** — naming the block says
you meant it there. The same key at the **top level** is quietly skipped for
modes that lack it, and nn says so:

```console
$ nn wrap --print
nn: note: not supported by `nono wrap`, ignored: trust_proxy_ca
nono wrap --profile go-dev -- go build
```

That's what lets one file serve both `nn` and `nn wrap`.

### Wrappers

Tools like [fnox](https://github.com/jdx/fnox) run above nono, so they can't
live in a profile:

```yaml
wrappers:
  - [fnox, exec, --]
```

Each entry is a command prefix used verbatim, including its own `--` — `direnv
exec .` doesn't take one.

### Environment

```yaml
env:
  NONO_THEME: minimal
env_unset:
  - AWS_PROFILE
```

These apply to the **outer** chain — your wrappers and nono itself. `NONO_*` and
`PATH` stop there and never reach the sandboxed child, which is exactly why they
belong here: profile `set_vars` refuses both.

For variables meant for the child, use `environment.set_vars` in `profile.json`.

`${VAR}` expands against your shell environment only, so
`PATH: "${HOME}/bin:${PATH}"` means the PATH you already had. An undefined
variable is an error, not an empty string. `$$` is a literal `$`.

Under [herdr](https://herdr.dev), nn sets `HERDR_AGENT` from the command it's
about to run. Opt out with `herdr: false`.

## Commands

| | |
|---|---|
| `nn [-- args]` | same as `nn run` |
| `nn run` / `nn shell` / `nn wrap` | pick the nono mode |
| `nn print` | show the command without running it (`--format shell\|lines\|json`) |
| `nn init` | write a starter config |
| `nn doctor` | check config, binaries and profile |
| `nn version` | version, commit and source date |

Handy flags: `--profile`, `--append`, `--no-wrappers`, `-e KEY=VALUE`,
`-u KEY`, `-c <path>`.

Everything after `--` goes to the child untouched, so `nn -- go test -v` never
lets nn see the `-v`.

## Troubleshooting

| Message | Fix |
|---|---|
| `no .nono/nn.yml found` | `nn init`, or `-c <path>` |
| `found .nono but no nn.yml in it` | `nn init` |
| `contains both nn.yml and nn.yaml` | delete one; nn won't guess |
| `profile: … does not exist` | check the filename — nn suggests near-misses like `.jsonc` |
| `unknown field "x"` | either a typo (nn suggests one) or a profile key |
| `not found on PATH` | `nn doctor` names the missing binary |

`nn print` shows the exact command, and `nn doctor` checks every piece of it.
Between them, most problems are one command away.

Exit codes: **2** for config or usage errors, **127** for a missing binary,
otherwise whatever the child returned.

## How it works

nn assembles the command line and `exec`s it, replacing itself. Nothing
supervises, so signals, the terminal and the exit code all behave exactly as if
you'd typed the long version — the same thing `fnox exec` and `nono wrap` do.

Paths in the config are made absolute before use, because nono runs from your
working directory, which may be far below the project root. `profile` and
`config` resolve against `.nono/`; everything else against the project root.

## Development

```sh
mise run build      # -> bin/nn
mise run check      # vet, gofmt, cross-compile, test
mise run repro      # verify the build is byte-for-byte reproducible
mise run install    # PREFIX=~/.local
```

Builds are reproducible: `-trimpath`, and a source date from
`SOURCE_DATE_EPOCH` or the commit rather than the wall clock.

The per-mode flag matrix is generated from `nono --help`, not transcribed, and a
test re-derives it from the installed nono so an upgrade that moves a flag fails
loudly. `syscall.Exec` can't be tested in-process, so the exec path is covered by
subprocess tests against stand-in binaries.

`mise.toml` pins Go and redirects `GOPATH` in-tree, because `~/go` isn't
writable under the nono profile this repo develops against.
