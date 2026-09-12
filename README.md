# nn

Stop typing nono.sh command lines. Start typing `nn`.

```sh
# before
fnox exec -- nono run --profile .nono/profile.json --allow-cwd --no-diagnostics --trust-proxy-ca -- claude

# after
nn
```

`nn` is a small CLI that drives [nono](https://nono.sh) from a config file: it
reads your flags from `.nono/nn.yml`, assembles that command line, and runs it.

## Install

```sh
go install github.com/jderuiter/nn/cmd/nn@latest
```

Requires [nono](https://nono.sh). From a clone: `mise run install`.

## Quick start

```sh
cd myproject
nn init            # writes .nono/nn.yml
```

A plain `nn.yml` at the top of the project works just as well — see
[Where the config lives](#where-the-config-lives).

Say which profile to use and what to run:

```yaml
# .nono/nn.yml
profile: profile.json      # a file next to this one, or a profile name
command: [claude]
allow_cwd: true

run:
  trust_proxy_ca: true
  no_diagnostics: true
```

Then:

```sh
nn
```

That runs `claude` in the sandbox your profile describes, from any subdirectory
of the repository — see [Where the config lives](#where-the-config-lives).

```sh
nn -- go build ./...     # run something else, this once
nn --append -- --resume  # add args to the configured command
nn shell                 # interactive shell in the sandbox
nn wrap -- go test       # direct mode, for scripts and CI
```

### Example: Claude Code in a sandbox

`.nono/profile.json` — the sandbox policy, extending the Claude Code pack:

```json
{
  "extends": "nolabs-ai/claude",
  "network": {
    "network_profile": "claude-code",
    "credentials": ["github"]
  }
}
```

The pack grants Claude its own config and state paths. `credentials` injects
GitHub auth at nono's proxy, so `gh` works inside the sandbox without a token
ever being readable there.

`.nono/nn.yml` — the flags that profile can't hold:

```yaml
profile: profile.json
command: [claude]
allow_cwd: true

run:
  trust_proxy_ca: true     # so tooling accepts the proxy's certificate
  no_diagnostics: true
```

### Example: letting it build

Claude in that sandbox can edit Go files but not build them. The toolchain and
the build cache sit outside the working directory, and `proxy.golang.org` is
not in the network profile. mise hits the same wall: it cannot write its own
state and cache dirs, so `mise run check` fails before it starts.

Both are grants on the profile, not flags on nn:

```json
{
  "extends": "nolabs-ai/claude",
  "groups": {
    "include": ["go_runtime", "go_runtime_macos", "mise_manager"]
  },
  "network": {
    "network_profile": "claude-code",
    "credentials": ["github"],
    "allow_domain": ["proxy.golang.org", "sum.golang.org"]
  },
  "filesystem": {
    "allow": ["$HOME/.local/state/mise", "$HOME/.cache/mise"]
  }
}
```

`nono profile groups <name>` prints the paths a group grants, and whether it
grants them read-only. `go_runtime` and `mise_manager` are read-only, which
covers the Go toolchain and the mise binary; `go_runtime_macos` adds write
access to `~/Library/Caches/go-build`. Nothing covers the dirs mise writes to,
hence the two `filesystem.allow` entries. On Linux, swap `go_runtime_macos` for
`go_runtime_linux`, or list both to keep one profile portable.

`nn.yml` doesn't change. `trust_proxy_ca` was already there, and it is what lets
`go mod download` accept the proxy's certificate. Check the result with a real
build:

```sh
nn -- go build ./...
```

## Configuration

Sandbox policy — filesystem, network, credentials, ports — belongs in
`profile.json`. `nn.yml` carries only what a profile can't express: nono flags
with no profile equivalent, and wrapper commands that run *above* nono. Put a
profile key here and nn tells you where it belongs instead.

Key names are nono's flag names with `-` as `_`, so `nono run --help` is the
reference. The ones you'll reach for:

| Key | Type | Does |
|---|---|---|
| `profile` | string | profile name, or a file next to the config |
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

`shell_bin` carries `--shell`, because `shell` names the shell-mode block.

### Where the config lives

The config can sit at the top of your project or inside `.nono/`, and nn looks
in two places for it: the directory you are in, and — when you are inside a git
repository — the repository root. `.yaml` works everywhere `.yml` does. First
match wins:

```
1. ./nn.yml                 5. <repo root>/nn.yml
2. ./nn.yaml                6. <repo root>/nn.yaml
3. ./.nono/nn.yml           7. <repo root>/.nono/nn.yml
4. ./.nono/nn.yaml          8. <repo root>/.nono/nn.yaml
```

So one config at the repository root serves every subdirectory, and a config in
a subdirectory overrides it for that directory. Outside a git repository only
the first four apply. nn does not search any further up: a `.nono/nn.yml` in
your home directory or in a parent project is never picked up by accident.

Both spellings in the *same* directory is an error — nn won't guess which you
meant, because edits to the loser would appear to do nothing.

Relative paths resolve against the config's own directory: `profile:
profile.json` names the file next to `nn.yml` in either layout. `--config
<path>` (or `$NN_CONFIG`) skips the search entirely and uses that file's
directory the same way.

### Per-mode blocks

`run`, `shell` and `wrap` accept different flags. Put mode-specific settings in
a block:

```yaml
profile: profile.json
allow_cwd: true            # applies everywhere it's supported

run:
  trust_proxy_ca: true     # run and shell only
wrap:
  command: [go, test, ./...]
```

A key in a block the mode rejects is an error. The same key at the top level is
skipped for modes that lack it, and nn says so — which is what lets one file
serve both `nn` and `nn wrap`. Move `trust_proxy_ca` up out of the `run:` block
and `nn wrap` drops it with a note instead of failing:

```console
$ nn wrap --print
nn: note: not supported by `nono wrap`, ignored: trust_proxy_ca
nono wrap --profile /myproject/.nono/profile.json -- go test ./...
```

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

These apply to the outer chain — your wrappers and nono itself. `NONO_*` and
`PATH` stop there and never reach the sandboxed child, which is why they belong
here: profile `set_vars` refuses both. For variables meant for the child, use
`environment.set_vars` in `profile.json`.

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

Flags: `--profile`, `--append`, `--no-wrappers`, `-e KEY=VALUE`, `-u KEY`,
`-c <path>`. Everything after `--` goes to the child untouched.

## Troubleshooting

| Message | Fix |
|---|---|
| `no nn.yml found` | `nn init`, or `-c <path>`; the message lists where nn looked |
| `found .nono but no nn.yml in it` | `nn init` |
| `contains both nn.yml and nn.yaml` | delete one; nn won't guess |
| config not picked up from a parent | it must be the repo root, or the current dir |
| `profile: … does not exist` | check the filename — nn suggests near-misses |
| `unknown field "x"` | a typo, or a key that belongs in the profile |
| `not found on PATH` | `nn doctor` names the missing binary |

Two commands cover most of it. `nn print` shows the exact command line nn will
run, without running it:

```console
$ nn print
nono run --profile /myproject/.nono/profile.json --allow-cwd --trust-proxy-ca --no-diagnostics -- claude
```

`nn doctor` checks every piece of that chain — the wrappers, nono, and the
profile via `nono profile validate`:

```console
$ nn doctor
  ok    config      /myproject/.nono/nn.yml
  ok    config parses and validates
  ok    nn run    builds
  ok    nono        /opt/homebrew/bin/nono (nono 0.76.0)
  ok    command     claude
  ok    profile     /myproject/.nono/profile.json
  ok    nn shell  builds
  ok    nn wrap   builds

all good
```

## Development

```sh
mise run build      # -> bin/nn
mise run check      # vet, gofmt, cross-compile, test
mise run repro      # verify the build is reproducible
mise run install    # PREFIX=~/.local
```

See [CLAUDE.md](CLAUDE.md) for architecture and design decisions.
