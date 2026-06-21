# codex-profiled

Run the same Codex configuration under different accounts.

`codex-profiled` lets you keep one Codex setup while using separate auth
profiles. Your Codex config, plugins, skills, history, and runtime state stay
shared; account auth stays isolated per profile.

## Install

Download binaries from [GitHub Releases](https://github.com/tarik02/codex-profiled/releases).
Windows, Linux, and WSL are supported.

```sh
nix run github:tarik02/codex-profiled
```

Or install the flake package:

```nix
inputs.codex-profiled.url = "github:tarik02/codex-profiled";
```

Codex itself is not bundled. Install `codex` separately.

## Usage

```sh
# pick or create a profile
codex-profiled

# run with a profile
codex-profiled @work
codex-profiled --profile work

# pass arguments to codex
codex-profiled resume abc
codex-profiled @work -- login
codex-profiled @work -- --help

# inspect profiles
codex-profiled list
codex-profiled current

# create or refresh shadow-home links without launching codex
codex-profiled materialize @work

# remember a profile for the current directory
codex-profiled set-default work

# delete a profile
codex-profiled delete work

# check local setup
codex-profiled doctor
```

The `default` profile always exists and uses Codex's normal auth file.

If a new profile has no auth yet, `codex-profiled` can run `codex login` for it.

## Profile Selection

When no profile is passed, selection order is:

1. `CODEX_PROFILE`
2. current directory default
3. interactive picker

Explicit CLI profiles always win:

```sh
codex-profiled @work
codex-profiled --profile work
```

Bare arguments are passed to Codex. Use `@profile` or `--profile` to select a
profile for one run.

Set a directory default:

```sh
codex-profiled set-default work
```

Show what would be used:

```sh
codex-profiled current --verbose
```

## Shadow Home Layout

Shared state lives in `CODEX_HOME` (default `~/.codex`, or
`%USERPROFILE%\.codex` on Windows).

Each non-default profile gets a persistent shadow home at:

```text
~/.codex-<profile>/
```

On Windows, the default profile path is:

```text
%USERPROFILE%\.codex-<profile>\
```

Example for profile `work`:

```text
~/.codex/                         # shared home
~/.codex-work/                    # shadow CODEX_HOME for profile work
  auth.json                       # real file, profile-private
  models_cache.json               # real file when present, not symlinked
  config.toml -> ~/.codex/config.toml
  sessions -> ~/.codex/sessions
  history.jsonl -> ~/.codex/history.jsonl
  log/                            # local runtime dir, not symlinked
  memories/                       # local runtime dir, not symlinked
  tmp/                            # local runtime dir, not symlinked
```

On Unix and on Windows systems where symlinks are available, shared entries are
created as symlinks. On Windows, if symlink creation is blocked, directories use
junctions and files use hard links.

Private entries:

- `auth.json`
- `models_cache.json`

Shadow-local runtime dirs (never symlinked from shared home):

- `log`
- `memories`
- `tmp`

Profile auth lives only in each profile shadow home:

```text
~/.codex-<profile>/auth.json
```

On each run, auth stays in the shadow home. It is not copied to or from
`~/.codex/auth-profiles`.

## Codex config profiles

Put profile overlays in the shadow home:

```text
~/.codex-<profile>/<profile>.config.toml
```

If that overlay exists, `codex-profiled` automatically passes `--profile <profile>` to
Codex for supported runtime commands (`exec`, interactive mode, etc.). For commands
that do not support `--profile`, it parses the overlay and appends equivalent
`--config` overrides instead. It skips injection when you already pass `--profile`.

Use `model_catalog_json` in the overlay to enable model selection in the Codex TUI.

Override the shadow root with:

```sh
export CODEX_SHADOW_ROOT="$HOME/.codex-profiles"
# -> $CODEX_SHADOW_ROOT/<profile>/
```

PowerShell:

```powershell
$env:CODEX_SHADOW_ROOT = "$env:USERPROFILE\.codex-profiles"
# -> $env:CODEX_SHADOW_ROOT\<profile>\
```

## Files

By default, profiles are stored under `~/.codex`:

```text
~/.codex/auth.json
~/.codex/profile-defaults.toml
~/.codex-<profile>/
```

On Windows, profiles are stored under `%USERPROFILE%` by default:

```text
%USERPROFILE%\.codex\auth.json
%USERPROFILE%\.codex\profile-defaults.toml
%USERPROFILE%\.codex-<profile>\
```

Useful environment variables:

- `CODEX_HOME`
- `CODEX_PROFILE`
- `CODEX_SHADOW_ROOT`
- `CODEX_BINARY`

## Release

Releases are managed by release-please and GoReleaser.

Use conventional commits on `master`; release-please opens a release PR. Merging
that PR creates the next version and publishes release artifacts.

## License

MIT
