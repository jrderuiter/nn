# AGENTS.md

Guidance for coding agents that work on `nn`.

## What this project is

`nn` is a Go command line tool. It reads `nn.toml` and turns each declared tool
into a fragment of a [nono](https://nono.sh) profile. It merges the fragments
into one profile, writes the files that the profile refers to, and runs `nono`.

`nn` generates profiles. It does not sandbox anything itself. Keep that line
clear when you add code: a tool describes access, and only nono enforces it.

Read `README.md` first. It explains the configuration format, every tool, and
the reasons behind the design.

## Layout

| Path | What lives there |
| --- | --- |
| `main.go` | The entry point. It calls `internal/cli`. |
| `internal/cli` | The cobra commands, and the pipeline that builds a run |
| `internal/config` | Loading and merging `nn.toml`, plus the environment layer |
| `internal/tool` | The provider contract and the registry. A provider is the code behind one tool. |
| `internal/tool/<name>` | One tool each: `runtime`, `git`, `github`, `kubernetes` |
| `internal/nono` | The profile types, the merger, and the call to `nono` |
| `internal/workspace` | The generated artifact directory under `.nono/nn` |
| `internal/secrets` | Resolving a secret with `fnox get` |

## Tasks

Tasks live in `mise.toml`. Run them with `mise run <task>`.

| Task | What it does |
| --- | --- |
| `build` | Build `bin/nn` |
| `test` | Run the unit and golden tests |
| `golden` | Rewrite the golden profiles after a change to a generator |
| `integration` | Run the tests that need the real `nono` binary |
| `lint` | Report unformatted files and vet problems |
| `fmt` | Format the source |
| `check` | Run `lint` and `test` together |

Run `mise run check` before you report that a change is done. If you changed a
generator, run `mise run golden` first, and read the diff.

## The golden tests

A golden test compares generated output against a stored copy of it, byte for
byte. Each directory under `internal/cli/testdata/cases` holds an `nn.toml` and
its fixtures. The test builds a profile for that case and compares it against
`internal/cli/testdata/golden/<case>.json`.

A changed golden file is the review. Read the diff and make sure that every
changed line is a change you meant to make. Never rewrite a golden file to make
a failing test pass.

A second test makes sure that no generated path is machine specific. The one
exception is a `credential_capture` command, which is host argv and passes
through verbatim.

The integration tests run `nono profile validate --strict` over every golden
profile. They need `nono` on the PATH, so they carry the `integration` build
tag and stay out of the default run.

## Adding a tool

A tool is one thing an agent can be given. Follow the shape of
`internal/tool/git/git.go`.

1. Create `internal/tool/<name>/<name>.go`.
2. Declare a `Config` struct. Give every field a `toml` tag and a `help` tag.
   The environment keys and `nn example` both come from those tags.
3. Call `tool.Register` from `init`, with a factory and a prototype function.
4. Implement `Name`, `Preflight` and `Build`.
5. Add the blank import to the provider block in `internal/cli/pipeline.go`.
6. Add the name to the `order` list in `internal/tool/tool.go`, so the merged
   profile stays byte stable.
7. Add a golden case under `internal/cli/testdata/cases`, then run
   `mise run golden`.
8. Document the tool in `README.md`.

`Build` returns a fragment and its artifacts. It never invokes nono, never
writes a file, and never resolves a secret. The pipeline does all three.

## Rules that the design depends on

Resolve a secret once, at launch, through `tool.Secret`. A backend that asks for
a touch or a password then asks at a moment the user can judge. A fetch on demand
asks in the middle of a session, next to whatever the agent was doing. Never
write a secret value to a file. Never list its variable in `allow_vars`.

Write every profile path relative to `$WORKDIR`, so the profile works on any
machine. `internal/workspace` holds the spelling.

Two tools that set the same key to different values are a configuration error.
The merger reports both names. Do not add a winner rule. The `[nono.profile]`
block is the only layer that overrides, because it is the user's last word.

Keep the tool order fixed. A generated profile must be the same bytes on every
run, or the golden tests lose their meaning.

## Style

Package comments say what the package is for. A comment on difficult code says
why the code is the way it is, not what it does. The existing comments are the
model. Match their density.

Error messages start with a lower case letter and name what failed, for example
`tool %q: %w`.

Documentation follows the plain English style of `README.md`: short sentences,
active voice, simple tenses, and no contractions.
