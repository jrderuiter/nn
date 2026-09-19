# nn

`nn` runs a command in a [nono](https://nono.sh) sandbox, built from the
tools you declare in one file.

```
nn -- claude
```

That reads `nn.toml`, turns each tool into a nono profile fragment,
merges the fragments into one profile, writes the files that profile refers to,
and runs `nono` with it.

## Why

With nono alone, giving an agent GitHub, git and Kubernetes access needs a long
command line, or one large profile that mixes unrelated concerns, or a pile of
small mixin profiles. Mixins cannot be templated, so a cluster name or a
context has to be written out by hand for every project.

`nn` does not replace profiles and does not re-implement sandboxing. It
generates profiles, so `nn profile` on its own stays useful.

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

`nn init` looks at the project and writes an `nn.toml` to match: the toolchains
it can see from `go.mod`, `package.json`, `.mise.toml` and the like, your git
identity, and the repository your origin remote points at. Anything that needs a
secret or a cluster is written out but left commented, so `nn -- claude` works
straight away and you turn the rest on when you are ready. Pass `--force` to
replace an existing file.

## Configuration

`nn` reads `~/.config/nn/config.toml` first, then the nearest `nn.toml` found by
walking up from the working directory, then the environment. Layers merge per
key, so a project file adds to a tool the user file declares rather than
replacing it. `nn example` prints a complete file to start from:

```
nn example > nn.toml
```

```toml
[nono]
agent   = "claude"
extends = ["jr/clean_env"]
groups  = ["unlink_protection"]
network_profile = "claude-code"

[tools.mise]
[tools.go]

[tools.git]
ssh   = true
name  = "Jane Doe"
email = "jane@example.com"

[tools.github]
secret = "GITHUB_TOKEN"

[tools.kubernetes]
context         = "prod-eks"
service_account = "claude-ro"
service_account_namespace = "apps"
```

Writing a `[tools.<name>]` section is what turns that tool on, which is why a
runtime that takes no settings is an empty section.

### Environment variables

Every value can also come from the environment: `NN_` plus the key path in
upper case, with dots as underscores.

| Variable | Sets |
| --- | --- |
| `NN_NONO_AGENT` | `nono.agent` |
| `NN_NONO_NETWORK_PROFILE` | `nono.network_profile` |
| `NN_NONO_EXTENDS` | `nono.extends`, comma separated |
| `NN_TOOLS_KUBERNETES_CONTEXT` | `tools.kubernetes.context` |
| `NN_TOOLS_KUBERNETES_SERVICE_ACCOUNT_NAMESPACE` | `tools.kubernetes.service_account_namespace` |
| `NN_TOOLS_GITHUB_SECRET` | `tools.github.secret` |
| `NN_TOOLS_MISE` | turns the `mise` tool on, or off with a false value |

The environment is applied after both files. `nn` matches these against the keys
it knows rather than parsing the variable name, because
`NN_NONO_NETWORK_PROFILE` would otherwise be ambiguous between
`nono.network_profile` and `nono.network.profile`.

A tool is turned on by the variable that names its section, so
`NN_TOOLS_MISE=true` is the environment's version of writing `[tools.mise]`.
That is the only way to enable a runtime, which has no settings of its own. A
false value (`false`, `0`, `no`, `off` or empty) removes the tool, which is how
a project default is dropped for one run. Turning a tool on never clears
settings its section already carries.

### Capabilities



| Tool | What it grants |
| --- | --- |
| `mise`, `go`, `node`, `bun`, `python`, `rust`, `java`, `nix` | One nono group each, plus the writable caches and environment variables that group omits |
| `git` | The host SSH agent socket, a committer identity, the git configuration group, extra hosts |
| `github` | The GitHub API, plus clone, fetch and push over HTTPS |
| `kubernetes` | One cluster, through nono's credential proxy |

### Secrets

`nn` never reads a secret value. It emits a nono `credential_capture` entry that
runs `fnox get <key>` on the host when the proxy needs the credential. The value
stays out of `nn`, out of the sandbox environment, and out of any generated
file. Inside the sandbox the agent only ever sees a phantom token.

Set the key up once with fnox, with any backend fnox supports:

```
fnox provider add op 1password --vault Engineering
fnox set GITHUB_TOKEN --provider op
```

### Kubernetes

nono has no Kubernetes feature, so `nn` assembles the access out of generic
nono parts.

With `service_account` set, `nn` writes a kubeconfig pointing at the real API
server, and a `credential_capture` entry that runs `kubectl create token` on the
host. nono mints the token outside the sandbox with your own cluster
credentials, caches it, intercepts the connection and adds it as a bearer
header. The kubeconfig holds no credential at all. This works for a cluster
whose kubeconfig uses an exec plugin, such as EKS or GKE, because the plugin
runs on the host.

Injecting a header means intercepting TLS, so kubectl is served a certificate
that nono signs. kubectl ignores the trust bundle variables that nono sets,
because Go reads the macOS system store instead, so `nn` passes
`--trust-proxy-ca`. nono then keeps one reusable authority in your user trust
store. The generated profile states no `ca_lifecycle`, because an explicit value
there contradicts the flag and nono refuses to start. Expect a keychain prompt the first
time. The cluster's own certificate is still checked, by nono, on the leg
between the proxy and the API server, using the authority that `nn` extracts
into `.nono/nn/kube/ca.pem`.

`nn` does not create service accounts and does not touch RBAC. The service
account and its permissions are what limit the agent, and they must already
exist.

`nn` does not generate per-endpoint proxy rules. Scoping a route by HTTP path
looks attractive but breaks the client: kubectl starts every command with
discovery calls such as `/api?timeout=32s` and `/openapi/v2`, and a rule list
that misses one turns into errors that read like a broken cluster. RBAC does
this job properly. `nn doctor` and every run mint a
token first, so a missing account or a missing permission is reported before the
agent starts rather than as a proxy warning afterwards.

If the context carries no certificate authority, `nn` says so before launch
rather than letting it appear later as
`TLS handshake failed: invalid peer certificate: UnknownIssuer`. Point
`cluster_ca` at a PEM file to supply one, or set `allow_missing_ca = true`
when the API server uses a publicly trusted certificate.

nono runs the token command with a stripped environment, where a version manager
shim cannot work. `nn` therefore resolves `kubectl` to a real binary and refuses
a shim with a message naming the problem. Set `kubectl` in the tool to an
absolute path to choose one yourself, which also keeps the generated profile the
same on every machine.

Without `service_account`, `nn` falls back to a plain kubeconfig carrying the
context's own credentials. Nothing is intercepted in that form, so the cluster
certificate verifies directly. It puts the credential inside the sandbox, and it
refuses to run for an exec plugin context.

### The escape hatch

A `[nono.profile]` block holds a raw profile fragment for anything that has no
tool of its own. Its keys are spelled exactly as they are in a nono profile, and it applies
after every tool, so it overrides them.

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
| `nn doctor` | Check the configuration and everything it depends on |
| `nn example` | Print a complete example `nn.toml` |

`nn init` writes exactly what a run writes, so the profile can be read, kept, or
handed to nono directly. `nn doctor` writes nothing: it loads the configuration,
runs every tool's own check, and validates the profile they produce.

By default `nn` hides nono's own capability table and its report of blocked
paths, so what you see is the output of the command you ran. Two flags bring
them back, named after the nono flags they control:

| nn flag | what it does | nono flag it drops |
| --- | --- | --- |
| `--banner` | show the capability table and status lines | `-s` |
| `--diagnostics` | show the paths the sandbox blocked | `--no-diagnostics` |

`-v` is separate: it reports what `nn` itself did, printing the profile path,
every generated file, the `WORKDIR` value and the exact `nono` command.
`--dry-run` prints that command instead of running it. `--tool` and `--no-tool`
narrow the run to some of the configured tools.

## Generated files

Everything `nn` generates lands in `.nono/nn/` inside the project, with a
`.gitignore` that excludes all of it:

```
.nono/nn/profile.json     the merged nono profile
.nono/nn/kube/config      the generated kubeconfig, mode 0600
.nono/nn/kube/ca.pem      the cluster certificate authority
.nono/nn/gh/              the gh CLI configuration, kept away from the host
```

## Portability

Every path in the generated profile is written relative to `$WORKDIR`, so the
file works on any machine. A credential route's `tls_ca` resolves that name from
the environment rather than expanding it, which is why `nn` sets `WORKDIR` on
the nono process. The one exception is a `credential_capture` command: that is
host argv, passed through verbatim, so paths in it are absolute. A golden test
fails if anything else leaks a machine specific path.

## Development

```
go test ./...                      unit and golden tests
go test ./internal/cli -update     rewrite the golden profiles
go test -tags integration ./...    checks that need the real nono binary
```

The golden tests build a profile for each case under
`internal/cli/testdata/cases` and compare it byte for byte. The integration
tests validate every golden profile with `nono profile validate --strict`, and
fail when a nono upgrade renames a key that `nn` generates.
