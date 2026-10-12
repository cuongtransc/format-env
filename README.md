# Format env
format-env is a Go command-line utility to generate and format environment files (.env) using a template. This tool allows you to specify the environment (e.g., dev, testing, staging, etc.) and a template file to generate the corresponding environment configuration file.

# Features
- Reads key-value pairs from an existing .env file.
- Applies the values to a template for formatting.
- Supports multiple stages (dev, testing, unstable, staging).
- Allows specifying the template path and stage dynamically through command-line arguments.

# Installation
Prebuilt binaries are published for macOS and Linux (Ubuntu, Debian, …) on
`amd64` and `arm64`.

## Option 1: mise (recommended)
With [mise](https://mise.jdx.dev) installed, this picks the right binary for
your OS and CPU and puts `fenv` on your `PATH`:
```sh
mise use -g github:cuongnbms/format-env
fenv            # prints usage
```

Install mise first if needed (Ubuntu or macOS):
```sh
curl https://mise.run | sh
echo 'eval "$(~/.local/bin/mise activate bash)"' >> ~/.bashrc   # zsh: ~/.zshrc and "activate zsh"
```

## Option 2: Download binary file
```sh
VERSION=v1.2.0
OS=$(uname -s | tr '[:upper:]' '[:lower:]')                     # linux | darwin
ARCH=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/') # amd64 | arm64
wget "https://github.com/cuongnbms/format-env/releases/download/${VERSION}/fenv_${OS}_${ARCH}" -O fenv
sudo install -m 0755 fenv /usr/local/bin/fenv
```

## Option 3: Compile from source
With [mise](https://mise.jdx.dev) installed (it provides the pinned Go toolchain):
```sh
mise run build                     # bin/fenv_{darwin,linux}_{amd64,arm64}
sudo install -m 0755 "bin/fenv_$(go env GOOS)_$(go env GOARCH)" /usr/local/bin/fenv
```

# Development
```sh
mise run ci      # gofmt check, go vet, tests
mise run fmt     # rewrite sources with gofmt
mise run build   # release binaries in bin/
```
# Usage

To use the utility, run it from the command line by specifying the template file and the environment stage you want to generate.
```
fenv <env_dir> <stages>
```
- env_dir: Path to the env dir (e.g., `env`). The template file name `_template.env` must have in this folder
- stages: The environment stage, separate by comma (e.g. `dev,testing,staging`).

Example
```
| env
|--- _template.env
|--- dev.env
|--- staging.env
|--- prod.env

# run format
fenv env dev,staging,prod
```

# Pre-commit

This repository can be used as a remote hook with the
[`pre-commit`](https://pre-commit.com) framework. Add the following config to
the project whose environment files should be formatted:

```yaml
repos:
  - repo: https://github.com/cuongnbms/format-env
    rev: v1.1.0
    hooks:
      - id: format-env
        args:
          - env
          - dev,test,prod
```

The first argument is the directory containing `_template.env`. The second is
the comma-separated list of stages to format. Install and verify the hook with:

```sh
pre-commit install
pre-commit run --all-files
```

When the hook changes an environment file, `pre-commit` stops the commit. Review
the changes, stage the files again, and retry the commit.

Use multiple hook entries when a project has more than one environment
directory:

```yaml
repos:
  - repo: https://github.com/cuongnbms/format-env
    rev: v1.1.0
    hooks:
      - id: format-env
        name: format service environment files
        args: [services/api/env, dev,test,prod]
      - id: format-env
        name: format worker environment files
        args: [services/worker/env, dev,prod]
```

# Template Syntax
The template file should use Go’s text/template syntax. For example:
```
# General
# =======================================================================
STAGE={{ .STAGE }}
PORT={{ df .PORT "8000" }}

# Database
# =======================================================================
DB_HOST={{ .DB_HOST }}
DB_PORT={{ .DB_PORT }}
DB_USER={{ .DB_USER }}
DB_PASS={{ .DB_PASS }}
DB_NAME={{ df .DB_NAME "example" }}

# Redis
# =======================================================================
REDIS_ENABLE_SSL={{ df .REDIS_ENABLE_SSL "false" }}
REDIS_URL={{ .REDIS_URL }}
```
Note: This template will render the environment variable `REDIS_ENABLE_SSL` and use a default value `false` if it is not provided.
