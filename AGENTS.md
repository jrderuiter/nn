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
| `internal/tool/<name>` | One tool each: `runtime`, `git`, `github`, `azuredevops`, `kubernetes` |
| `internal/nono` | The profile types, the merger, and the call to `nono` |
| `internal/workspace` | The generated artifact directory under `.nono/nn` |
| `internal/secrets` | Resolving a secret with `fnox get` |

## Worktrees

Do all work in a git worktree. Do not change files in the main checkout,
because it can hold uncommitted work of the user.

1. For a new branch, run
   `git worktree add .worktrees/<branch> -b <branch> --no-track origin/main`.
2. For an existing branch, run `git worktree add .worktrees/<branch> <branch>`.
3. Run every command for the branch from inside its worktree.
4. When you finish, run `git worktree remove .worktrees/<branch>`.

## Tasks

Tasks live in `mise.toml`. Run them with `mise run <task>`.

| Task | What it does |
| --- | --- |
| `build` | Build `bin/nn` |
| `test` | Run the unit and golden tests |
| `golden` | Rewrite the golden profiles after a change to a generator |
| `integration` | Run the tests that need the real `nono` binary |
| `packs` | Install the nono packs that the golden profiles extend |
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
tag and stay out of the default run. Some golden profiles extend an agent pack,
so run `mise run packs` before the first run. CI runs them against the nono
version that `mise.toml` pins, and a weekly job tries the latest nono. In CI a
missing `nono` fails the tests instead of skipping them.

## Adding a tool

A tool is one thing an agent can be given. Follow the shape of
`internal/tool/git/git.go`.

1. Create `internal/tool/<name>/<name>.go`.
2. Declare a `Config` struct. Give every field a `toml` tag and a doc comment.
   The environment keys come from the `toml` tags. Do not use the key
   `enabled`: the config layer reserves it to switch a table off, and removes
   it before the provider decodes the table. Apply a string default when the
   decoded value is empty, so that a later layer can give the default back.
3. Call `tool.Register` from `init`, with a factory and a prototype function.
4. Implement `Name`, `Preflight` and `Build`.
5. Add the blank import to the provider block in `internal/cli/pipeline.go`.
6. If the tool must run before others, add its name to the `order` list in
   `internal/tool/tool.go`. A tool that is not in the list runs after the
   listed ones, in name order, so the merged profile stays byte stable.
7. Add the tool and every setting to `nn example` in
   `internal/cli/example.go`. A test fails when one is missing.
8. Add a golden case under `internal/cli/testdata/cases`, then run
   `mise run golden`.
9. Document the tool in `README.md`.

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

Follow these rules for every comment that you add:

1. Keep it short. One or two sentences are usually enough.
2. Write for the reader of the code, not about your change. Do not describe
   the fix or the history.
3. Keep only details that the code does not show. If a reader can see it in
   the code, delete the comment.
4. Write in plain English, as in the documentation.

Put guidance for coding agents in `AGENTS.md` only, not in code comments or
`README.md`. Add it only when an agent cannot do the work correctly without it.

Error messages start with a lower case letter and name what failed, for example
`tool %q: %w`.

Documentation follows the plain English style of `README.md`: short sentences,
active voice, simple tenses, and no contractions.
