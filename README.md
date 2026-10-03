# nn

`nn` runs a command in a [nono](https://nono.sh) sandbox, using a profile built 
from the tools you declare in one file: `nn.toml`.

```
nn run -- claude
```

Goal is to reduce the complexity of configuring nono profiles and make it easy
to spin up a sandbox with a simpler declarative syntax.

## Why

To give an agent GitHub, git and Kubernetes access with nono alone, you need a
long command line. The other ways are one large profile that mixes unrelated
concerns, or a pile of small mixin profiles. A mixin has no templates, so you
write a cluster name or a context by hand for every project.

`nn` does not replace profiles and does not re-implement sandboxing. It
generates them. `nn init` writes the profile and stops, so you can read it or
give it to nono yourself.

## Install

### Prerequisites

`nn` needs the following tools on your `PATH`:

- [nono](https://nono.sh) is required to run sandboxes.
- [fnox](https://fnox.jdx.dev) is required when you use the `github` or `azure_devops` tool to resolve secrets.
- `kubectl` is required when you use the `kubernetes` tool.

### Installation

Install `nn` with Go:

```
go install github.com/jrderuiter/nn@latest
```

Or download a release binary. A version tag such as `v0.1.0` makes CI publish a
GitHub release. The release holds a static binary for macOS and Linux, on
amd64 and arm64, and a `checksums.txt`. The repository is private, so a
download needs a GitHub token with read access to its contents:

```
gh release download --repo jrderuiter/nn --pattern 'nn_linux_amd64' --output /usr/local/bin/nn
chmod +x /usr/local/bin/nn
```

## Getting started

Follow these steps to set up and run an agent.

### 1. Initialize the project

Run `nn init` in your project directory:

```
nn init
```

`nn init` writes an example `nn.toml` when the directory does not have one, and
generates the sandbox files in `.nono/nn/`. If an `nn.toml` already exists, `nn
init` leaves it unchanged and regenerates the sandbox files.

### 2. Configure `nn.toml`

Open `nn.toml` and configure the agent and tools you need. A minimal file with
Claude, git, and GitHub looks like this:

```toml
[nono]
network_profile = "minimal"

[agents.claude]
extends = ["nolabs-ai/claude"]
network_profile = "claude-code"

[tools.git]
name  = "Jane Doe"
email = "jane@example.com"

[tools.github]
secret = "GITHUB_TOKEN"
```

If your configuration extends an agent pack, install it with nono first:

```
nono pull nolabs-ai/claude
```

If your configuration uses secrets, set up [fnox](https://fnox.jdx.dev) if you
have not already done so, then store the secret:

```
fnox init
fnox set GITHUB_TOKEN
```

See the [fnox documentation](https://fnox.jdx.dev) for supported secret providers,
such as 1Password or system keychains.

### 3. Check the configuration

Run `nn doctor` to verify your setup:

```
nn doctor
```

`nn doctor` loads the configuration, tests tool settings, and validates the
generated profiles without running the command. It reports any missing tools or
unresolved secrets before you start a session.

### 4. Run the agent

Start your agent in the sandbox:

```
nn run -- claude
```

`nn` builds the merged nono profile, writes the required configuration files to
`.nono/nn/`, and executes the command inside nono.

## Commands

| Command | What it does |
| --- | --- |
| `nn run -- <cmd>` | Generate the sandbox files and run the command |
| `nn run --agent <name> -- <cmd>` | Run the command in the sandbox of an agent |
| `nn init` | Generate the sandbox files and stop |
| `nn profile` | Print the generated profile |
| `nn doctor` | Make sure that the configuration works |
| `nn example` | Print a complete example `nn.toml` |

`nn init` writes exactly what a run writes, so you can inspect the profile or
pass it to nono yourself. `nn init` also writes the example `nn.toml` when the
project does not have one.

`nn doctor` writes nothing. It loads the configuration, tests tool settings, and
makes sure that generated profiles are valid. It checks the shared profile and
every agent profile, or one agent when you pass `--agent`.

`--agent` works with every command. For example, `nn profile --agent agy` prints
the profile for agy, and `nn init --agent agy` writes it.

`nn profile` prints the profile to stdout and writes nothing. Because it creates
no directories, it can show a cache grant that `nn run` drops when a directory
cannot be created.

Add `--tool <name>` to include only specific tools in the generated profile. If
you name a tool that `nn.toml` does not enable, `nn` stops with an error.

`--as-mixin` leaves out the base layer and `[nono]` settings. The output holds
only what the tools add, so another profile can extend it.

```
nn profile --tool kubernetes --as-mixin > kubernetes.json
```

By default, `nn run` hides nono's capability banner and diagnostic reports. Two
flags restore them:

| Flag | Description | Drops nono flag |
| --- | --- | --- |
| `--banner` | Show the capability table and status lines | `-s` |
| `--diagnostics` | Show blocked sandbox paths | `--no-diagnostics` |

`-v` prints verbose execution details, including generated file paths and the
exact `nono` command. `--dry-run` prints the `nono` command without running it.

## How it works

`nn` does not sandbox processes itself. It generates profiles and configuration
files, then delegates enforcement to nono:

1. **Profile generation:** `nn` reads `nn.toml`, merges tool fragments into a
   single profile, and writes it to `.nono/nn/profile.json` (or
   `.nono/nn/profile-<agent>.json` when targeting an agent). Paths in the profile
   are written relative to `$WORKDIR`, so profiles remain portable across
   machines. On Linux, the profile also includes nono's `linux_temp_read` group.
   nono lets a process write `/tmp` on Linux but not read it, and a build that
   reads back its own temporary files then fails. macOS already grants this
   read access.
2. **Support files:** When tools require local files, `nn` creates them under
   `.nono/nn/`. For example, it writes a scoped `kube/config` and cluster CA for
   Kubernetes, or isolated `gh/` settings for GitHub. `nn` places a `.gitignore`
   in `.nono/nn/` to exclude generated files from version control, and removes
   files when you disable their tool.
3. **Secret resolution:** `nn` resolves secrets on the host with `fnox get`
   before launching the agent. Secrets never touch disk or the sandbox. The
   agent receives phantom tokens, and nono's credential proxy injects the real
   credentials into outbound HTTPS requests.
4. **Nono execution:** `nn` calls `nono run` with the generated profile,
   required flags (such as `--trust-proxy-ca` on macOS for intercepted routes),
   and the target command.

## Concepts

### Configuration files

`nn` reads configuration from three layers, in this order:

1. User defaults from `~/.config/nn/config.toml`.
2. Project configuration from the nearest `nn.toml` found by walking up from the
   current working directory.
3. Environment variables.

Layers merge per key. A project file adds to a tool that the user file declares;
it does not replace the tool.

When the upward search does not find an `nn.toml`, `nn run` and `nn doctor` stop
with an error. `nn init` writes an example file in the working directory.
Running `nn init` in a subdirectory of an existing project does not create a
file, because a second file would hide the one above it.

`nn example` prints a complete example file with every setting commented. You can
use it to create a new file or compare your project configuration against it:

```
nn example > nn.toml
nn example | diff nn.toml -
```

### Environment variables

Every configuration setting can also come from the environment: `NN_` plus the
key path in upper case, with dots as underscores.

| Variable | Sets |
| --- | --- |
| `NN_NONO_NETWORK_PROFILE` | `nono.network_profile` |
| `NN_NONO_EXTENDS` | `nono.extends`, comma separated |
| `NN_NONO_ALLOW_DOMAIN` | `nono.allow_domain`, comma separated |
| `NN_TOOLS_KUBERNETES_CONTEXT` | `tools.kubernetes.context` |
| `NN_TOOLS_KUBERNETES_SERVICE_ACCOUNT_NAMESPACE` | `tools.kubernetes.service_account_namespace` |
| `NN_TOOLS_GITHUB_SECRET` | `tools.github.secret` |
| `NN_TOOLS_MISE` | turns the `mise` tool on, or off with a false value |

`nn` applies environment variables after reading configuration files. It matches
these names against known keys. An `[agents.<name>]` section does not support
environment variables to avoid ambiguity.

The variable naming a tool section enables that tool. For example,
`NN_TOOLS_MISE=true` turns on the `mise` tool. A false value (`false`, `0`,
`no`, `off`, or empty) removes the tool, allowing you to disable a project
default for a single run. A true value never clears configuration that the
section already carries.

### Secrets

`nn` uses [fnox](https://fnox.jdx.dev) to manage credentials. You never write
secrets into `nn.toml` or environment files. Instead, you declare the secret key
name in `nn.toml`, and store the real value in fnox on the host. Check the
reference for each tool to see which secrets it requires.

To set a secret, initialize fnox if you have not already done so, and store the
key:

```
fnox init
fnox set GITHUB_TOKEN
```

fnox supports multiple backends, including 1Password, age encryption, Bitwarden,
and system keychains. To store a secret with a specific provider:

```
fnox provider add op 1password --vault Engineering
fnox set GITHUB_TOKEN --provider op
```

When you start an agent session, `nn` resolves the required secrets once on the
host using `fnox get <key>`. Resolving up front is a deliberate security decision:
you authorize access (such as a fingerprint touch or master password) at launch
time, when you can review the session context. This avoids unexpected
authorization prompts while an agent runs autonomously.

The generated profile does not allow any secret variables through to the
sandbox, and `nn` never writes secrets to disk. Instead, the agent sees a phantom
token, which is a stand-in value. Nono's credential proxy intercepts outbound
requests on the host and exchanges the phantom token for the real secret.

Kubernetes authentication is different: nono mints tokens dynamically with
`kubectl create token` and renews them as they expire. This does not require
approval and is not stored in fnox.

### Profile escape hatch

`nn` merges fragments from each tool into a single nono profile. If two tools set
the same key to different values, `nn` stops with a configuration error and names
both tools rather than picking a winner.

The `[nono.profile]` block is the exception. It holds a raw profile fragment for
anything without a dedicated tool. Its keys match the field names of a nono
profile. The block applies after every tool, so it serves as your last word.

```toml
[nono.profile.network]
deny_domain = ["*.ads.example.com"]

[nono.profile.filesystem]
read = ["$HOME/.config/some-tool"]
```

The block overrides single values, such as `workdir.access`, and individual keys
in a map, such as an environment variable in `set_vars`.

Lists behave differently. The block adds its entries to the list that the tools
built; it cannot remove an entry. To restrict access, write a deny rule, such as
`filesystem.deny`, `network.deny_domain`, or `environment.deny_vars`.

## Configuration reference

### Full example

Here is a full `nn.toml` that demonstrates the available sections:

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

### nono

The `[nono]` section sets shared configuration for the sandbox:

| Key | Description |
| --- | --- |
| `extends` | List of nono profiles to extend. Merged before tool fragments. |
| `groups` | List of nono policy groups to include by name. |
| `network_profile` | One of nono's network profiles (e.g., `minimal`, `developer`). An empty value leaves egress unrestricted. |
| `allow_domain` | List of extra domains to allow, in addition to the network profile and tool rules. |

Without a `network_profile`, nono leaves egress unrestricted. Naming one narrows
the sandbox. `minimal` grants access to LLM APIs and nothing else; it is what
`nn init` and `nn example` write. Each tool allows the hosts it needs on top of
that profile, and `allow_domain` adds any others the project needs.

To override settings or supply raw nono configuration directly, use the
`[nono.profile]` block (see [Profile escape hatch](#profile-escape-hatch)).

### Agents

An `[agents.<name>]` section defines settings for a specific agent command, such
as `claude` or `agy`. The section name matches the command binary. When you run
that command (or pass `--agent <name>`), its settings merge into `[nono]`:

- `extends`, `groups`, and `allow_domain` append to the shared `[nono]` values.
- `network_profile` replaces the `[nono]` network profile.
- Tools and `[nono.profile]` apply to all agents equally.
- `[agents.<name>.profile]` supplies a raw profile fragment for that agent.

| Key | Description |
| --- | --- |
| `extends` | Agent pack profiles to extend (e.g., `["nolabs-ai/claude"]`). |
| `network_profile` | Network allowlist for this agent (e.g., `claude-code`). |
| `allow_domain` | Extra domains allowed for this agent. |
| `groups` | Extra nono policy groups for this agent. |

To run an arbitrary command inside an agent's sandbox, pass `--agent`:

```
nn run --agent claude -- bash
```

#### Claude Code

Claude Code requires the `nolabs-ai/claude` pack and the `claude-code` network
profile:

```toml
[agents.claude]
extends = ["nolabs-ai/claude"]
network_profile = "claude-code"
```

Install the pack with `nono pull nolabs-ai/claude` before running the agent.

#### Antigravity (agy)

Antigravity requires API access to Google endpoints and an ephemeral port range
for its local language server:

```toml
[agents.agy]
extends = ["nolabs-ai/antigravity"]
allow_domain = ["cloudcode-pa.googleapis.com", "oauth2.googleapis.com"]

[agents.agy.profile.network]
# Dynamic port range for the language server (49152-65535 on macOS, 32768-60999 on Linux).
open_port_range = [[49152, 65535]]
```

Install the pack with `nono pull nolabs-ai/antigravity` before running the agent.

#### OpenAI Codex

Codex requires the `nolabs-ai/codex` pack and the `codex` network profile:

```toml
[agents.codex]
extends = ["nolabs-ai/codex"]
network_profile = "codex"
```

Install the pack with `nono pull nolabs-ai/codex` before running the agent.

### Tools

| Tool | What it grants |
| --- | --- |
| `mise`, `go`, `node`, `bun`, `python`, `rust`, `java`, `nix` | One nono group each, plus writable caches and environment variables |
| `git` | A committer identity, git configuration, and extra hosts |
| `github` | The GitHub API, plus clone, fetch, and push over HTTPS |
| `azure_devops` | The Azure DevOps API, plus clone, fetch, and push over HTTPS |
| `kubernetes` | Access to one or more clusters through nono's credential proxy |

#### Runtimes

Language runtimes and version managers require no configuration. Adding an empty
section turns the runtime on:

```toml
[tools.mise]
[tools.go]
[tools.python]
```

Each runtime includes its corresponding nono group and configures writable cache
and state directories under the project or user cache.

The `mise` tool has one setting:

| Key | Description |
| --- | --- |
| `trust_workdir` | Trust the mise configuration files in the working directory, inside the sandbox only (default: `false`). |

mise trusts a configuration file by its path. A new git worktree is a new path,
so mise refuses its `mise.toml` even when you trust the main repository. With
`trust_workdir = true`, nn sets `MISE_TRUSTED_CONFIG_PATHS` to `$WORKDIR` in the
sandbox. The trust state of mise on the host does not change.

```toml
[tools.mise]
trust_workdir = true
```

#### Git

The `git` tool configures git commit identity and repository access:

| Key | Description |
| --- | --- |
| `name` | Author and committer name inside the sandbox. |
| `email` | Author and committer email address. |
| `hosts` | Extra git server domains to allow. |
| `config` | Allow reading the host git configuration file (default: `true`). |
| `worktree` | Grant the shared git directory of a linked worktree (default: `true`). |

```toml
[tools.git]
name   = "Jane Doe"
email  = "jane@example.com"
hosts  = ["git.example.com"]
config = true
```

A linked worktree, which `git worktree add` creates, keeps its objects, refs and
configuration in the `.git` directory of the main repository. That directory is
outside the working directory, so git fails in the sandbox without a grant. When
the working directory is a linked worktree, nn passes `--allow <dir>` to nono
for that `.git` directory. The grant is a flag and not a profile entry, because
the path is different on each machine. nn does not grant the checkout of the
main repository. Set `worktree = false` to turn the grant off.

#### GitHub

The `github` tool configures GitHub API and git access over HTTPS:

| Key | Description |
| --- | --- |
| `secret` | Name of the fnox key holding your personal access token (required). |
| `git` | Enable git clone, fetch, and push over HTTPS (default: `true`). |
| `rewrite_ssh` | Automatically rewrite SSH git remotes to HTTPS (default: `true`). |
| `gh_cli` | Redirect GitHub CLI config and cache into `.nono/nn/` (default: `true`). |

```toml
[tools.github]
secret = "GITHUB_TOKEN"
```

#### Azure DevOps

The `azure_devops` tool configures access to Azure DevOps (`dev.azure.com`) using
a personal access token (PAT):

| Key | Description |
| --- | --- |
| `secret` | Name of the fnox key holding your PAT (required). |
| `organization` | Azure DevOps organization name (detected from git remote if omitted). |
| `project` | Default project for `az devops` commands (detected from git remote if omitted). |
| `projects` | Additional project names to allow cloning over rewritten HTTPS URLs. |
| `rewrite_ssh` | Automatically rewrite SSH remotes to HTTPS (default: `true`). |
| `az_cli` | Configure `az devops` defaults and cache under `.nono/nn/` (default: `true`). |

```toml
[tools.azure_devops]
secret       = "AZURE_DEVOPS_PAT"
organization = "my-org"
project      = "My Project"
```

The credential proxy works over HTTPS, so `nn` automatically rewrites SSH
remotes to HTTPS to authenticate with your PAT. If the agent must clone
additional projects from the organization that are not configured as remotes,
add them to `projects`.

#### Kubernetes

The `kubernetes` tool provides scoped access to a Kubernetes cluster through
nono's credential proxy:

| Key | Description |
| --- | --- |
| `auth` | Authentication mode: `"service-account"` or `"host"` (required). |
| `context` | Context name from your host kubeconfig (required for `host`, optional for `service-account`). |
| `service_account` | Name of the service account to mint tokens for (required for `service-account`). |
| `service_account_namespace` | Namespace of the service account (default: `"default"`). |
| `token_ttl` | Lifetime of minted service account tokens (default: `"1h"`). |
| `kubeconfig` | Path to host kubeconfig (default: `~/.kube/config`). |
| `kubectl` | Path to host `kubectl` binary (must be a real binary, not a shim). |
| `cluster_ca` | Path to PEM CA certificate if the kubeconfig context lacks one. |
| `allow_missing_ca` | Allow clusters without a CA certificate (default: `false`). |
| `current` | The cluster that the sandbox kubeconfig selects (required with more than one cluster). |
| `clusters` | One table for each cluster, keyed by its context name in the sandbox. |

**Service account mode (recommended):**
Mints short-lived tokens on the host using your own credentials and injects them
via proxy:

```toml
[tools.kubernetes]
auth                      = "service-account"
context                   = "prod-eks"
service_account           = "agent-reader"
service_account_namespace = "apps"
```

**Host mode (local clusters only):**
Copies context credentials (such as client certificates) directly into the
sandbox. Use only for local test clusters (e.g., k3d):

```toml
[tools.kubernetes]
auth    = "host"
context = "k3d-dev"
```

**More than one cluster:**
Declare one table under `clusters` for each cluster. The key of the table is
the context name in the sandbox, so the agent runs `kubectl --context prod`.
Each table holds `auth`, `context`, `service_account`, `cluster_ca` and
`allow_missing_ca` for its cluster. The other keys in `[tools.kubernetes]` are
defaults that every cluster shares, and a cluster table can override them.

```toml
[tools.kubernetes]
kubectl = "/opt/homebrew/bin/kubectl"
current = "local"

[tools.kubernetes.clusters.prod]
auth            = "service-account"
context         = "prod-eks"
service_account = "agent-reader"

[tools.kubernetes.clusters.local]
auth    = "host"
context = "k3d-dev"
```

`nn` writes one kubeconfig that holds every cluster. Each service account
cluster gets its own token, proxy route and certificate authority file. nono
mints each token at launch, so a backend that asks for a touch asks once for
each cluster.

nono picks a proxy route by the host of the API server. If two clusters use
the same API server and one of them uses `"service-account"`, `nn` stops with
an error. The `clusters` tables come from `nn.toml` only, because they have no
spelling as one environment variable.

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
