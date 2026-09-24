# nn

`nn` runs a command in a [nono](https://nono.sh) sandbox, built from the
tools you declare in one file.

```
nn run -- claude
```

That reads `nn.toml` and turns each tool into a nono profile fragment. It merges
the fragments into one profile, writes the files that profile refers to, and
runs `nono`.

## Why

To give an agent GitHub, git and Kubernetes access with nono alone, you need a
long command line. The other ways are one large profile that mixes unrelated
concerns, or a pile of small mixin profiles. A mixin has no templates, so you
write a cluster name or a context by hand for every project.

`nn` does not replace profiles and does not re-implement sandboxing. It
generates them. `nn init` writes the profile and stops, so you can read it or
give it to nono yourself.

## Install

```
go install github.com/jrderuiter/nn@latest
```

A version tag such as `v0.1.0` makes CI publish a GitHub release. The release
holds a static binary for macOS and Linux, on amd64 and arm64, and a
`checksums.txt`. The repository is private, so a download needs a GitHub token
with read access to its contents:

```
gh release download --repo jrderuiter/nn --pattern 'nn_linux_amd64'
```

To install `nn` in a Docker image, pass the token as a build secret. A build
secret does not stay in any image layer.

```dockerfile
# syntax=docker/dockerfile:1
FROM debian:bookworm-slim
ARG NN_VERSION=v0.1.0
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates jq \
 && rm -rf /var/lib/apt/lists/*
RUN --mount=type=secret,id=gh_token \
    token="$(cat /run/secrets/gh_token)" \
 && api="https://api.github.com/repos/jrderuiter/nn/releases/tags/${NN_VERSION}" \
 && asset="$(curl -fsSL -H "Authorization: Bearer $token" "$api" \
      | jq -r ".assets[] | select(.name == \"nn_linux_${TARGETARCH}\") | .url")" \
 && curl -fsSL -H "Authorization: Bearer $token" -H "Accept: application/octet-stream" \
      -o /usr/local/bin/nn "$asset" \
 && chmod +x /usr/local/bin/nn
```

```
docker build --secret id=gh_token,env=GH_TOKEN .
```

`nn` needs `nono` on the PATH. The `github` tool needs
[fnox](https://fnox.jdx.dev), and the `kubernetes` tool needs `kubectl`.

## Getting started

```
nn init
nn doctor
nn run -- claude
```

`nn init` writes the example `nn.toml` when the directory has none, and then
generates the sandbox files from it. Every other command stops with an error
when it finds no `nn.toml`, so `nn init` is the step that starts a project. An
`nn.toml` that already exists is used as it is, and is never rewritten.

Edit the file. Make sure that it works with `nn doctor`. Then run the agent.
The file has every key, the optional ones commented. `nn example` prints the
same file, so you can compare a project file against it later:

```
nn example | diff nn.toml -
```

## Configuration

An agent pack goes in an `[agents.<name>]` section, so one project can run
several agents. The section is described under [Agents](#agents).

When the command after `--` is a known agent (`claude`, `codex` or `agy`), `nn`
sets `HERDR_AGENT` to that name in the environment it hands to nono. The
variable is not in `allow_vars`, so it stops at nono and never reaches the
sandbox. It names the command only and grants nothing.

Without a `network_profile`, nono leaves egress unrestricted, so naming one
narrows the sandbox. `minimal` grants the LLM APIs and nothing else, and it is
what `nn init` and `nn example` write. Each tool allows the hosts it needs on top of that,
and `allow_domain` adds any others the project needs.

`nn` reads `~/.config/nn/config.toml` first, then the nearest `nn.toml` found by
walking up from the working directory, then the environment. Layers merge per
key, so a project file adds to a tool that the user file declares. It does not
replace the tool. When that upward search finds no `nn.toml`, a run and
`nn doctor` stop with an error, and `nn init` writes the example one in the
working directory. A subdirectory of a project that already
has one gets nothing, because a second file there would hide the file above it.
`nn example` prints a complete file to start from:

```
nn example > nn.toml
```

```toml
[nono]
extends = ["jr/clean_env"]
groups  = ["unlink_protection"]
network_profile = "minimal"
allow_domain = ["proxy.golang.org"]

[agents.claude]
extends = ["nolabs-ai/claude"]
network_profile = "claude-code"

[tools.mise]
[tools.go]

[tools.git]
name  = "Jane Doe"
email = "jane@example.com"

[tools.github]
secret = "GITHUB_TOKEN"

[tools.kubernetes]
auth            = "service-account"
context         = "prod-eks"
service_account = "claude-ro"
service_account_namespace = "apps"
```

A `[tools.<name>]` section turns that tool on. A runtime takes no configuration,
so its section is empty.

### Agents

An `[agents.<name>]` section holds what only one agent needs: its pack, its
hosts, and its network profile. The name is the name of the agent command.

```toml
[nono]
network_profile = "minimal"

[agents.claude]
extends = ["nolabs-ai/claude"]
network_profile = "claude-code"

[agents.agy]
extends = ["nolabs-ai/antigravity"]
allow_domain = ["cloudcode-pa.googleapis.com", "oauth2.googleapis.com"]

[agents.agy.profile.network]
open_port_range = [[49152, 65535]]
```

`nn` compares the base name of the command with the section names, so
`~/.local/bin/claude` selects `[agents.claude]`. The section adds its
`extends`, `groups` and `allow_domain` to the `[nono]` section. Its
`network_profile` replaces the one in `[nono]`. The tools and `[nono.profile]`
apply to every agent in the same way.

An `[agents.<name>.profile]` block is a raw profile fragment for one agent, in
the same spelling as `[nono.profile]`. It merges like a tool, so a key that a
tool sets to a different value is an error.

agy starts a language server on a random local port, and has no option for a
fixed one. macOS picks that port from 49152 to 65535, so the example opens that
range. A port in the range lets agy connect as well as listen, so agy can also
reach any other local service that listens there. The range keeps out the fixed
ports of common local services, such as 5432 for Postgres and 9222 for the
Chrome debugger. Do not use `open_port = [0]`, which opens every local port. On
Linux, the random range is 32768 to 60999 by default.

A command without a section, such as `kubectl`, gets the `[nono]` section and
the tools, and no agent pack. To run a command in the sandbox of an agent, name
the agent with `--agent`. If no section has that name, `nn` stops with an
error. A misspelled name would otherwise start the command without its pack.

If a known agent (`claude`, `codex` or `agy`) has no section, `nn` prints a
warning and runs it with the shared profile. A project that still has the
pack in `[nono]` `extends` then works as before.

```
nn run -- agy
nn run -- kubectl cluster-info
nn run --agent claude -- bash
```

Keep each pack in its own section. If you put two packs in `extends`, each
agent gets the files and credentials of the other agent.

Install a pack with `nono pull` before you extend it. nono does not grant the
hosts that agy uses in any of its network profiles, so its section adds them.

### Environment variables

Every value can also come from the environment: `NN_` plus the key path in
upper case, with dots as underscores.

| Variable | Sets |
| --- | --- |
| `NN_NONO_NETWORK_PROFILE` | `nono.network_profile` |
| `NN_NONO_EXTENDS` | `nono.extends`, comma separated |
| `NN_NONO_ALLOW_DOMAIN` | `nono.allow_domain`, comma separated |
| `NN_TOOLS_KUBERNETES_CONTEXT` | `tools.kubernetes.context` |
| `NN_TOOLS_KUBERNETES_SERVICE_ACCOUNT_NAMESPACE` | `tools.kubernetes.service_account_namespace` |
| `NN_TOOLS_GITHUB_SECRET` | `tools.github.secret` |
| `NN_TOOLS_MISE` | turns the `mise` tool on, or off with a false value |

`nn` applies the environment after both files. It matches these names against
the keys it knows, and does not read the variable name itself. An
`[agents.<name>]` section has no environment variables.
`NN_NONO_NETWORK_PROFILE` is otherwise ambiguous between `nono.network_profile`
and `nono.network.profile`.

The variable that names a tool's section turns the tool on, so
`NN_TOOLS_MISE=true` does the same as a `[tools.mise]` section. It is the only
way to turn on a runtime, which has no configuration of its own. A false value
(`false`, `0`, `no`, `off` or empty) removes the tool, so you can drop a project
default for one run. A true value never clears the configuration that the
section already carries.

### Tools

| Tool | What it grants |
| --- | --- |
| `mise`, `go`, `node`, `bun`, `python`, `rust`, `java`, `nix` | One nono group each, plus the writable caches and environment variables that group omits |
| `git` | A committer identity, the git configuration group, extra hosts |
| `github` | The GitHub API, plus clone, fetch and push over HTTPS |
| `azure_devops` | The Azure DevOps API, plus clone, fetch and push over HTTPS, for one organization |
| `kubernetes` | One cluster, through nono's credential proxy |

### Secrets

`nn` resolves a secret once, with `fnox get <key>`, while you are starting the
agent, and hands it to nono in its environment. It never writes the value to a
file and never lets it into the sandbox: the generated profile allows no such
variable through, and the agent sees a phantom token, which is a stand-in that
the proxy exchanges for the real value.

Resolving up front is the point. A backend that asks for a touch or a password
asks at launch, which is a moment you can judge. Fetching on demand would ask
in the middle of a session, next to whatever the agent was doing, and teach you
to approve a secret whenever an agent asks for one.

Store a token as the bare value. A git route authenticates with basic auth,
and nono sends the stored value as the user and password pair, so `nn` adds the
user name itself: `x-access-token` for GitHub, and an empty name for Azure
DevOps.

The Kubernetes token is different: nono mints it with `kubectl create token` and
renews it as it expires, which needs no approval and cannot be done once at
launch.

Create the key once with fnox, with any backend that fnox supports:

```
fnox provider add op 1password --vault Engineering
fnox set GITHUB_TOKEN --provider op
```

### Azure DevOps

The `azure_devops` tool gives the agent one organization on `dev.azure.com`.
Git and the REST API use the same host, and both take a personal access token
(PAT) as a basic auth password. So one proxy route covers git, the API and the
`az devops` extension. Inside the sandbox, `AZURE_DEVOPS_EXT_PAT` holds a
phantom token. `AZURE_CONFIG_DIR` and `AZURE_DEVOPS_CACHE_DIR` point into the
artifact directory, because the sandbox cannot write the default locations.
`nn` also writes the `az devops` defaults there, so a command needs no `--org`
or `--project` flag.

`nn` takes `organization` and `project` from the Azure DevOps remotes of the
repository. There is no default. If no remote names one, or if the remotes name
more than one, set the key in `nn.toml`, or the run stops with an error. Only
the `az devops` defaults use `project`, so with `az_cli = false` you can leave
it out.

```toml
[tools.azure_devops]
secret = "AZURE_DEVOPS_PAT"
# Only needed when the remotes do not name exactly one of each.
organization = "my-org"
project = "My Project"
```

The proxy can only add a credential to an HTTPS request, so `nn` rewrites ssh
remotes to HTTPS. An ssh remote has the form
`git@ssh.dev.azure.com:v3/{org}/{project}/{repo}`, but the HTTPS form puts
`_git` between the project and the repository. Git can only replace a fixed
start of a URL, so `nn` writes one rewrite per project. It takes the projects
from the remotes of the current repository. A remote can also use a host alias
that ends in `.ssh.dev.azure.com`, such as `team.ssh.dev.azure.com`, and `nn`
rewrites it in that spelling. If the agent must clone a project
that is not a remote, add it to `projects`. `nn` only rewrites the projects of
that organization, because the token belongs to that organization.

The tool does not cover the older `{org}.visualstudio.com` host.

### Kubernetes

nono has no Kubernetes feature, so `nn` builds the access out of generic parts.

The `auth` key picks how the agent authenticates, and it is required. Its value
is `service-account` or `host`. The key has no default, so the configuration
always states the form.

With `auth = "service-account"`, you must also set `service_account`. `nn`
writes a kubeconfig pointing at the real API server and a `credential_capture`
that runs `kubectl create token` on the host. nono mints the token outside the
sandbox with your own credentials. It then intercepts the connection and adds
the token as a bearer header. The kubeconfig itself holds no credential. A
cluster with an exec plugin, such as EKS or GKE, works, because the plugin runs
on the host.

With `auth = "host"`, `nn` writes a plain kubeconfig that carries the
credentials of the context, for example a client certificate and its key. The
agent can read them and use them outside the sandbox until they expire. Use
this form only for a local test cluster, such as one from k3d. It does not work
for an exec plugin context, and `nn` refuses it together with `service_account`.

A local cluster listens on this machine, for example on `127.0.0.1:6550`. Go
never sends a loopback address through a proxy, so kubectl connects to that
port directly. `nn` then opens the port with `open_port` instead of allowing the
host. On macOS, `open_port` also lets the sandbox listen on the port, but the
cluster already holds it.

```toml
[tools.kubernetes]
auth    = "host"
context = "k3d-dev"
```

`nn` never creates service accounts or RBAC, and generates no per-endpoint
rules. The permissions of the account are what limit the agent. Two keys exist
because of how the pieces fit together. `kubectl` must name a real binary, not a
version manager shim, because nono runs the token command with a stripped
environment. `cluster_ca` supplies the cluster authority when the context
carries none, and `allow_missing_ca = true` says the API server is publicly
trusted.

nono must intercept TLS to inject a header. On macOS that needs
`--trust-proxy-ca`, which `nn` passes for you. The cluster's own certificate is
still verified, by nono, on the leg to the API server.

### Intercepted connections

A credential route means nono intercepts TLS, so the client is served a
certificate that nono signs. Most clients follow the trust bundle variables
that nono sets. On macOS a Go client such as `gh` or `kubectl` reads the system
trust store instead and rejects the connection, so `nn` passes
`--trust-proxy-ca` there whenever the profile has any route. nono then keeps one
reusable authority in your trust store. Expect a keychain prompt the first time.

The failure without it is misleading: `gh` reports `The token in GITHUB_TOKEN is
invalid` for what is really a certificate it cannot verify.

`nn` passes the flag on macOS only. Go reads the trust bundle variables on
other systems, and nono defines no such argument there, so passing it would
stop the run with `unexpected argument '--trust-proxy-ca'`.

### The escape hatch

A `[nono.profile]` block holds a raw profile fragment for anything that has no
tool of its own. Its keys carry the same names as in a nono profile. The block
applies after every tool, so the block overrides the tools.

```toml
[nono.profile.network]
deny_domain = ["*.ads.example.com"]

[nono.profile.filesystem]
read = ["$HOME/.config/some-tool"]
```

Two tools that set the same key to different values are a configuration error,
and `nn` names both of them rather than picking a winner. The `[nono.profile]`
block is the one exception, because it is your own last word.

The block overrides a single value, such as `workdir.access`, and an entry of
a map, such as a variable in `set_vars`. A list works differently. The block
adds its entries to the list that the tools built, and it cannot remove one.
To take access away, write a deny rule, for example `filesystem.deny`,
`network.deny_domain` or `environment.deny_vars`.

## Commands

| Command | What it does |
| --- | --- |
| `nn run -- <cmd>` | Generate the sandbox files and run the command |
| `nn run --agent <name> -- <cmd>` | Run the command in the sandbox of an agent |
| `nn init` | Generate the sandbox files and stop |
| `nn profile` | Print the generated profile |
| `nn doctor` | Make sure that the configuration works |
| `nn example` | Print a complete example `nn.toml` |

`nn init` writes exactly what a run writes, so you can read the profile, keep
it, or give it to nono yourself. `nn init` also writes the example `nn.toml` when
the project has none. `nn doctor` writes nothing, so it never creates that file. It loads the
configuration, tests every tool, and makes sure that the profiles they produce
are valid. It checks the shared profile and the profile of every agent, or one
agent when you pass `--agent`.

`--agent` works with every command. `nn profile --agent agy` prints the profile
of agy, and `nn init --agent agy` writes it.

`nn profile` prints the profile to stdout and writes nothing. Because it
creates no directory, it can show a cache grant that `nn run` then drops: when
`nn run` cannot create a cache or state directory that a tool asks for, it
removes that grant and prints a warning.

Add `--tool` once for each tool, and the profile holds only those tools. If you
name a tool that `nn.toml` does not enable, `nn` stops with an error.

`--as-mixin` leaves out the base layer and the `[nono]` settings. The output
then holds only what the tools add, and another profile can extend it. A mixin
cannot carry nono flags. If the tools add a credential route, add
`--trust-proxy-ca` to the nono command yourself. If the tools use the generated
files, the mixin keeps the grant for `.nono/nn`, and you must run `nn init`
first.

```
nn profile --tool kubernetes --as-mixin > kubernetes.json
```

By default `nn run` hides nono's own capability table and its report of blocked
paths. You see the output of the command you ran. Two flags of `nn run` bring
them back, named after the nono flags they control:

| nn flag | what it does | nono flag it drops |
| --- | --- | --- |
| `--banner` | show the capability table and status lines | `-s` |
| `--diagnostics` | show the paths the sandbox blocked | `--no-diagnostics` |

`-v` is separate. It reports what `nn` did: the profile path, every generated
file, the `WORKDIR` value and the exact `nono` command.
`--dry-run` prints that command instead of running it.

## Generated files

Everything `nn` generates lands in `.nono/nn/` inside the project, with a
`.gitignore` that excludes all of it:

```
.nono/nn/profile.json     the merged nono profile, for a command with no agent section
.nono/nn/profile-<agent>.json
                          the profile of one agent
.nono/nn/kube/config      the generated kubeconfig, mode 0600
.nono/nn/kube/ca.pem      the cluster certificate authority
.nono/nn/gh/              the gh CLI configuration, kept away from the host
```

When `nn run` or `nn init` writes the files, it also removes every entry in
`.nono/nn/` that no configured tool uses. If you turn off the kubernetes tool,
the next run removes `kube/`. The `.gitignore` and the profile files always
stay, because another agent can be running with its profile.

## Portability

`nn` writes every path in the profile relative to `$WORKDIR`, so the file works
on any machine. The `tls_ca` key of a credential route reads that name from the
environment instead of expanding it, so `nn` sets `WORKDIR` on the nono process.
A `credential_capture` command is the one exception. It is host argv, passed
through verbatim, so paths in it are absolute. A golden test fails if anything
else leaks a machine-specific path.

## Development

Tasks live in `mise.toml`:

```
mise run build         build bin/nn
mise run dist          build for macOS and Linux, amd64 and arm64
mise run test          unit and golden tests
mise run golden        rewrite the golden profiles
mise run integration   tests that need the real nono binary
mise run lint          gofmt and go vet
mise run check         lint and test together
```

The golden tests build a profile for each case under
`internal/cli/testdata/cases` and compare it byte for byte. The integration
tests make sure that every golden profile is valid, with
`nono profile validate --strict`. They fail when a nono upgrade renames a key
that `nn` generates.
