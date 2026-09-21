# nn

`nn` runs a command in a [nono](https://nono.sh) sandbox, built from the
tools you declare in one file.

```
nn -- claude
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

`nn` needs `nono` on the PATH. The `github` tool needs
[fnox](https://fnox.jdx.dev), and the `kubernetes` tool needs `kubectl`.

## Getting started

```
nn init
nn doctor
nn -- claude
```

`nn init` writes a minimal `nn.toml` when the directory has none, and then
generates the sandbox files from it. `nn -- claude` writes the same file on a
first run, so a project needs no setup step of its own. An `nn.toml` that
already exists is used as it is, and is never rewritten.

Edit the file. Make sure that it works with `nn doctor`. Then run the agent.
`nn example` prints a complete configuration with every key, the optional ones
commented:

```
nn example > nn.toml
```

## Configuration

An agent pack is an ordinary entry in `extends`: `nn` has no separate key for
it, because nono has no agent concept. When the command after `--` is a known
agent (`claude` or `codex`), `nn` sets `HERDR_AGENT` to that name in the
environment it hands to nono. The variable is not in `allow_vars`, so it stops
at nono and never reaches the sandbox. It names the command only and grants
nothing, so the profile stays the same whatever you run.

Without a `network_profile`, nono leaves egress unrestricted, so naming one
narrows the sandbox. `minimal` grants the LLM APIs and nothing else, and it is
what `nn init` and `nn example` write. Each tool allows the hosts it needs on top of that,
and `allow_domain` adds any others the project needs.

`nn` reads `~/.config/nn/config.toml` first, then the nearest `nn.toml` found by
walking up from the working directory, then `nn.local.toml` beside it, then the
environment. Layers merge per key, so a project file adds to a tool that the
user file declares. It does not replace the tool. When that upward search finds
no `nn.toml`, `nn init` and a run write a minimal one in the working directory.
A subdirectory of a project that already has one gets nothing, because a second
file there would hide the file above it. `nn example` prints a complete file to
start from:

```
nn example > nn.toml
```

```toml
[nono]
extends = ["nolabs-ai/claude", "jr/clean_env"]
groups  = ["unlink_protection"]
network_profile = "minimal"
allow_domain = ["proxy.golang.org"]

[tools.mise]
[tools.go]

[tools.git]
name  = "Jane Doe"
email = "jane@example.com"

[tools.github]
secret = "GITHUB_TOKEN"

[tools.kubernetes]
context         = "prod-eks"
service_account = "claude-ro"
service_account_namespace = "apps"
```

A `[tools.<name>]` section turns that tool on. A runtime takes no configuration,
so its section is empty.

### Machine differences

Commit `nn.toml`, and put whatever differs per machine in `nn.local.toml` beside
it. Add that name to `.gitignore`. The local file merges into the committed one
per key, so it changes one setting and leaves the rest of the table alone:

```toml
# nn.toml, committed
[tools.kubernetes]
service_account = "claude-ro"

# nn.local.toml, yours only
[tools.kubernetes]
kubectl = "/opt/homebrew/bin/kubectl"
```

A list is the exception, because a later layer replaces a list rather than
adding to it. The local file belongs to the `nn.toml` that was found, so `nn`
does not look for it on its own.

### Environment variables

Every value can also come from the environment: `NN_` plus the key path in
upper case, with dots as underscores.

| Variable | Sets |
| --- | --- |
| `NN_NONO_NETWORK_PROFILE` | `nono.network_profile` |
| `NN_NONO_EXTENDS` | `nono.extends`, comma separated |
| `NN_NONO_ALLOW_DOMAIN` | `nono.allow_domain`, comma separated |
| `NN_TOOLS_KUBERNETES_CONTEXT` | `tools.kubernetes.context` |
| `NN_TOOLS_KUBERNETES_IN_CLUSTER` | `tools.kubernetes.in_cluster` |
| `NN_TOOLS_KUBERNETES_SERVICE_ACCOUNT_NAMESPACE` | `tools.kubernetes.service_account_namespace` |
| `NN_TOOLS_GITHUB_SECRET` | `tools.github.secret` |
| `NN_TOOLS_MISE` | turns the `mise` tool on, or off with a false value |

`nn` applies the environment after both files. It matches these names against
the keys it knows, and does not read the variable name itself.
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

The Kubernetes token is different: nono mints it with `kubectl create token` and
renews it as it expires, which needs no approval and cannot be done once at
launch.

Create the key once with fnox, with any backend that fnox supports:

```
fnox provider add op 1password --vault Engineering
fnox set GITHUB_TOKEN --provider op
```

### Kubernetes

nono has no Kubernetes feature, so `nn` builds the access out of generic parts.

With `service_account` set, `nn` writes a kubeconfig pointing at the real API
server and a `credential_capture` that runs `kubectl create token` on the host.
nono mints the token outside the sandbox with your own credentials. It then
intercepts the connection and adds the token as a bearer header. The kubeconfig
itself holds no credential. A cluster with an exec plugin, such as EKS or GKE,
works, because the plugin runs on the host. Without `service_account`, `nn`
writes a plain kubeconfig that carries the credentials of the context. That form
puts them inside the sandbox, and it does not work for an exec plugin context.

#### In a pod

A pod has no kubeconfig. It has the identity the kubelet mounts at
`/var/run/secrets/kubernetes.io/serviceaccount`: a bearer token, the cluster
certificate authority, and the namespace. Set `in_cluster` and `nn` builds the
same access out of those instead.

```
NN_TOOLS_KUBERNETES_IN_CLUSTER=true
```

Set it from the pod spec, so one committed `nn.toml` serves a laptop and a pod.
`nn` never detects the mode on its own. A guess would make the same command
reach a different cluster in a different place, with nothing in the file to say
so.

The credential form does not change. The capture command reads the mounted token
on the host side, once a minute, which is how the rotation the kubelet performs
reaches the proxy. The token never enters the sandbox, and the sandbox needs no
`kubectl` to get one. The certificate authority is the mounted `ca.crt`, so
neither `cluster_ca` nor `allow_missing_ca` is needed.

The API server is `https://kubernetes.default.svc`, not the address in
`KUBERNETES_SERVICE_HOST`. It is a name rather than a cluster IP, which is what
the allowlist and the TLS interception both want. `api_server` overrides it.

`service_account` still narrows the identity, but only when it names an account
other than the one the pod already runs as. `nn` reads that name from the
mounted token. Minting for a different account needs `kubectl` in the image and
RBAC on `serviceaccounts/token`. Naming the pod's own account changes nothing,
so the key is safe to leave in a shared file.

Four keys are refused with `in_cluster`, because each describes a kubeconfig
that a pod does not have: `context`, `kubeconfig`, `cluster_ca` and
`allow_missing_ca`. An empty environment value drops one for a single run, for
example `NN_TOOLS_KUBERNETES_CONTEXT=`. A file meant for both places is simpler
without `context`, since an unset `context` already means the current one.
`token_ttl` applies only when `nn` mints a token, not to the mounted one.

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

## Commands

| Command | What it does |
| --- | --- |
| `nn -- <cmd>` | Generate the sandbox files and run the command |
| `nn run -- <cmd>` | The same, spelled out for scripts |
| `nn init` | Generate the sandbox files and stop |
| `nn doctor` | Make sure that the configuration works |
| `nn example` | Print a complete example `nn.toml` |

`nn init` writes exactly what a run writes, so you can read the profile, keep
it, or give it to nono yourself. Both write a minimal `nn.toml` when the project
has none. `nn doctor` writes nothing, so it never creates that file. It loads the
configuration, tests every tool, and makes sure that the profile they produce
is valid.

By default `nn` hides nono's own capability table and its report of blocked
paths. You see the output of the command you ran. Two flags bring
them back, named after the nono flags they control:

| nn flag | what it does | nono flag it drops |
| --- | --- | --- |
| `--banner` | show the capability table and status lines | `-s` |
| `--diagnostics` | show the paths the sandbox blocked | `--no-diagnostics` |

`-v` is separate. It reports what `nn` did: the profile path, every generated
file, the `WORKDIR` value and the exact `nono` command.
`--dry-run` prints that command instead of running it. `--tool` and `--no-tool`
narrow the run to some of the configured tools.

## Generated files

Everything `nn` generates lands in `.nono/nn/` inside the project, with a
`.gitignore` that excludes all of it:

```
.nono/nn/profile.json     the merged nono profile
.nono/nn/kube/config      the generated kubeconfig, mode 0600
.nono/nn/kube/ca.pem      the cluster certificate authority
.nono/nn/kube/host.yaml   the kubeconfig the token command uses, in a pod
.nono/nn/gh/              the gh CLI configuration, kept away from the host
```

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
