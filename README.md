# codex-profiled

Run the same Codex configuration under different accounts.

`codex-profiled` lets you keep one Codex setup while using separate auth
profiles. Your Codex config, plugins, skills, history, and runtime state stay
shared; account auth stays isolated per profile.

## Install

Download binaries from [GitHub Releases](https://github.com/tarik02/codex-profiled/releases).

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

## Files

By default, profiles are stored under `~/.codex`:

```text
~/.codex/auth.json
~/.codex/auth-profiles/<profile>.auth.json
~/.codex/profile-defaults.toml
```

Useful environment variables:

- `CODEX_HOME`
- `CODEX_PROFILE`
- `CODEX_PROFILE_ROOT`
- `CODEX_BINARY`

## Release

Releases are managed by release-please and GoReleaser.

Use conventional commits on `master`; release-please opens a release PR. Merging
that PR creates the next version and publishes release artifacts.

## License

MIT
