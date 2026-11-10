# Contributing

Thanks for helping. Kluard is small and standard-library only, so getting started is quick.

## Set up

```bash
git clone https://github.com/getkluard/kluard.git
cd kluard
go version        # 1.26 or newer
make test         # gofmt check, go vet, go test ./...
```

No dependencies are downloaded: the project uses only the Go standard library. Keep it that way unless there is a strong reason.

To run the whole stack locally you need Docker: `./dev.sh` (see the README).

## Before you open a pull request

- `make test` passes. CI also runs the race detector, cross-builds the agent and checks the SQL against a real ClickHouse
  engine (`pip install chdb`, see "Development" in the README).
- New behaviour has tests. Look at `internal/server/*_test.go` for HTTP tests with fake stores and `internal/backup` for a fake database.
- UI is a single file, `internal/ui/index.html`. There is no build step. Text goes through the `el()` helper, which never
  interprets data as HTML: keep it that way, never use `innerHTML` with data.
- Anything that touches tenants, permissions or secrets needs a test that proves another tenant or a read-only user is refused.
- Update the README and `CHANGELOG.md` if behaviour changes.
- Scripts must stay executable in git: `git update-index --chmod=+x path/to/script.sh`.

## Commit messages

Short imperative subject (`Add host settings page`), then a paragraph on why if it is not obvious.

## Reporting bugs and ideas

Use the issue templates. For security problems see [SECURITY.md](SECURITY.md) and do not open a public issue.

## Sign your commits (DCO)

Kluard uses the [Developer Certificate of Origin](DCO) instead of a contributor agreement. It says that you wrote the change
or have the right to submit it under the licence of the file. You confirm it by adding a `Signed-off-by` line to every commit:

```bash
git commit -s -m "Add host settings page"      # adds: Signed-off-by: Your Name <you@example.com>
git rebase --signoff main                       # sign commits you already made
```

Use your real name and an e-mail address you can be reached on. A check on every pull request refuses commits without it.

## Licences

By contributing you agree that your contribution is licensed under the Apache License 2.0, as the rest of the core is.

Files in the `ee/` directory and files named `*_ee.go` or `*_ee_test.go` are the Enterprise Edition under [ee/LICENSE](ee/LICENSE).
We do not accept outside contributions to them unless we have agreed it with you in writing first; ask in an issue before you start.

The name Kluard and its logo are not covered by the Apache licence. See [TRADEMARKS.md](TRADEMARKS.md).
