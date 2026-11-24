# format-env — notes for coding agents

`fenv` is a single-file Go CLI (`fenv.go`) that renders `.env` stage files from an
`_template.env`. The tests are `go test ./...` over `fenv_test.go`,
`features_test.go` and `hook_test.go`.

## CI levels

DLV-002. The whole check set runs in about a second, so all three levels run the
same steps: `gofmt -l .` has to come back empty (`fmt:check`), then `go vet ./...`
(`lint`), then `go test ./...` (`test`). The three bodies live in `mise.toml`.

| Level | Runs | Cost |
| --- | --- | --- |
| `ci:fast` | `fmt:check`, `lint`, `test`, in that order; takes no machine slot | 0.41 / 0.34 / 0.33 s — 3 runs, 2026-11-25, ct-dev, warm build cache |
| `ci` | `ci:fast` inside the machine slot | the same steps; one run, 2026-11-25, ct-dev |
| `ci:full` | `ci` inside the machine slot | equal to `ci` |

`ci:fast` starts with `.mise/lib/ci-slot --own-tree` and ends by marking that tree
green (slot runtime version 4, the copy `ct ci install` writes). `ci` and
`ci:full` start with their slot guard and run `ci:fast` inside the slot. There is
no `depends` on any level.

Where it runs: ct-dev or the work machine — this repo has no pipeline. Nothing
runs `ci:full` after a merge yet: it is run by hand. The nightly is not registered
yet (owner, `ct ci nightly add`). `ci:full` does not include `test:e2e`,
`test:load`, `smoke` or `uat`; this repo defines none of them.

The budgets are the DLV-002 defaults (`ci:fast` ≤ 60 s, `ci` ≤ 5 min, `ci:full` ≤
30 min); every level is far under them, so no selection or sharding is planned.

The pre-commit hook (`.githooks/pre-commit`, wired once by `mise run setup`) runs
`ci:fast`. It is offline and needs no services, so it stays the whole hook.
