# What nn relies on in nono

Written against nono 0.76.0 and fnox 1.35.2. Each item is something `nn` depends
on, so a nono upgrade that changes one of them breaks `nn`.

## Profile schema

- `nono profile schema` prints the authoritative JSON Schema, and
  `nono profile guide` prints the authoring guide. Both come from the binary,
  not from the network.
- The top level sets `additionalProperties: false`. An unknown key is a hard
  error, which is why every field in `internal/nono/profile.go` uses
  `omitempty`.
- A `filesystem` entry and a `groups` entry are each either a bare string or an
  object with a `when` predicate. An empty `when` array is a parse error.
- An `allow_domain` entry is a bare string until it carries endpoint rules.

## Credential routes

- A route in `network.custom_credentials` stays inert unless its map key also
  appears in `network.credentials`. Forgetting this is silent: the profile
  validates and the route simply never fires.
- `env_var` is required when `credential_key` uses the `cmd://` scheme.
- `credential_capture` runs its command on the host, outside the sandbox, with
  the user's own credentials. That is what makes `kubectl create token` and
  `fnox get` work for a sandboxed agent.
- A sandboxed process reaches an injected service by talking to the real
  upstream URL through the proxy variables that nono sets. The documented
  path-prefix form is not usable, see the spike results below, so no port ever
  has to be pinned.

## Paths and grants

- nono does not mount. It grants capabilities over real host paths.
- A granted path that does not exist is dropped silently. A cache directory
  therefore has to exist before launch, or the tool inside the sandbox cannot
  write to it. `nn` creates those directories, the same thing the nolabs-ai
  claude pack does in its `ensure-dirs.sh` session hook.
- A granted path that exists but cannot be read is a hard startup failure, with
  a "Failed to canonicalize path" message.
- Matching a directory grants it recursively, so `$WORKDIR/.nono/nn` covers
  everything `nn` writes.
- The required `deny_credentials` group denies `~/.kube`, `~/.aws` and friends,
  and cannot be removed with `groups.exclude`. This is why the kubernetes
  capability generates its own kubeconfig instead of exposing the host one.

## Variables

The only templating is a fixed list of variables, expanded in `filesystem`
paths, in `command_args`, and in `environment.set_vars` values but not keys:

`$HOME`, `$WORKDIR`, `$TMPDIR`, `$UID`, `$XDG_CONFIG_HOME`, `$XDG_DATA_HOME`,
`$XDG_STATE_HOME`, `$XDG_CACHE_HOME`, `$XDG_RUNTIME_DIR`, `$NONO_CONFIG`,
`$NONO_PACKAGES`, `$PACK_DIR` and `~`.

There is no `${...}` form, no conditionals and no user defined names. That gap
is the reason `nn` exists.

The list above covers the fields that expand these names themselves. Some other
fields validate a `$NAME` against the environment instead, and refuse to start
when it is unset:

```
nono: Environment variable 'WORKDIR' validation failed: not set
```

`network.custom_credentials.*.tls_ca` behaves this way, so `nn` sets `WORKDIR`
on the nono process and keeps the path relative. Treat `tls_client_cert` and
`tls_client_key` the same way.

A `credential_capture` command is different again: it is host argv, passed
through verbatim with no expansion at all, so any path in it must be absolute.

## Profile names

- `extends` accepts a namespaced name such as `nolabs-ai/claude` or `jr/mise`.
- `--profile` does not. A namespaced name there is read as a registry pack
  reference and fails with a 404.
- `nn` therefore always writes a file and passes `--profile <path>`, with the
  namespaced names in the `extends` list of that file.
- There is no stdin input for profiles.

## Merge order in nono

- Arrays append and deduplicate across `extends`.
- Maps merge, and the child wins.
- Scalars: the child wins.
- `network.block` is sticky true. `workdir.access` inherits the base when the
  child says `none`. `open_urls` replaces the base entirely when the child
  mentions it at all.

`nn` mirrors these rules for its own fragments in `internal/nono/merge.go`, with
one difference: where nono lets a later layer win, `nn` reports a conflict,
because two capabilities disagreeing is a configuration error rather than an
override.

## Confirmed by scripts/spike.sh

Run against nono 0.76.0 on macOS.

- The credential proxy works through the standard proxy variables. nono sets
  `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` in the child, intercepts TLS, and
  injects the configured header on a request to the real upstream URL. A
  request to `https://httpbin.org/headers` came back carrying the injected
  header.
- The path-prefix form `http://127.0.0.1:<port>/<service>/` does not work this
  way. It answered `{"error":"Unauthorized"}` with HTTP 401, both without a
  `Proxy-Authorization` header and with the phantom token as a bearer. nn does
  not use it.
- `network.open_port` makes no difference to the path-prefix form, which fails
  either way.
- A host that no `allow_domain` entry covers is blocked. A request to
  `https://example.com` failed to connect at all.
- nono sets `<SERVICE>_BASE_URL` in the child, for example
  `PROBE_BASE_URL=http://127.0.0.1:19999/probe`, and puts the per-session
  phantom token in the route's `env_var`.
- `credential_capture` runs on the host, outside the sandbox, as the real user,
  but with a stripped environment. A version manager shim such as
  `~/.local/share/mise/shims/kubectl` fails there, and the only symptom is
  `credential capture command failed with exit code 2` in a `tls_intercept`
  warning. Name a real binary by absolute path.
- A kubeconfig with an empty user makes kubectl fall back to an interactive
  `Please enter Username:` prompt on the first 401. The generated kubeconfig
  uses an exec plugin that prints the phantom token instead.
- `endpoint_rules` on a credential route scopes it by HTTP method and path. It
  is easy to get wrong for a real client: kubectl begins every command with
  discovery requests such as `/api?timeout=32s`, `/apis?timeout=32s`,
  `/version` and `/openapi/v2`, and a rule list that misses one fails in a way
  that looks like a broken cluster. nn generates no endpoint rules.
- kubectl writes a discovery cache. Left alone it uses `~/.kube/cache`, which
  the required `deny_credentials` group blocks, so `KUBECACHEDIR` has to be
  redirected as well.

The practical result: a sandboxed client talks to the real upstream URL and lets
the proxy do the work. Nothing has to know the proxy port.

## TLS interception

- nono intercepts TLS only for a host that has a credential route. A plain
  `allow_domain` host tunnels through untouched, and its certificate verifies
  normally.
- On the client leg, the default `ca_lifecycle: "session"` writes a per-run
  authority and points `SSL_CERT_FILE`, `CURL_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`
  and `NODE_EXTRA_CA_CERTS` at it. curl follows those. kubectl does not, because
  Go on macOS reads the system trust store instead, and the request fails with
  `received fatal alert: BadCertificate` on nono's side and
  `x509: certificate signed by unknown authority` on kubectl's.
- `nono run --trust-proxy-ca` keeps one reusable authority in the macOS user
  trust store, which Go does read. That is what the kubernetes capability uses.
- Leave `ca_lifecycle` out of the profile when passing that flag. An explicit
  `session` contradicts it and nono refuses to start:
  `profile requests network.tls_intercept.ca_lifecycle=session but
  --trust-proxy-ca requests trusted`.
- On the upstream leg, `tls_ca` is what lets the proxy verify a cluster with a
  private authority. It is trusted in addition to the system roots.
- `--proxy-ca-cert` and `--proxy-ca-key`, which would pin a reusable authority
  from a file, exist only on `nono proxy`, not on `nono run`.
- There is no option anywhere to skip upstream verification. A private
  authority must be supplied.
