# Contributing

This is the service behind [Toskar](https://github.com/yeixio/toskar-core)'s community model ratings. It is part of the Toskar project, and the same rules apply: [Code of Conduct](https://github.com/yeixio/toskar-core/blob/main/CODE_OF_CONDUCT.md), [Contributor License Agreement](https://github.com/yeixio/toskar-core/blob/main/CLA.md), and [governance and roles](https://github.com/yeixio/toskar-core/blob/main/GOVERNANCE.md). By opening a pull request you agree to the CLA.

## Setup

Go, at the version in `go.mod`.

```bash
go test -race ./...
RATINGS_SECRET="$(openssl rand -hex 32)" RATINGS_DB=./ratings.db go run ./cmd/toskar-ratings serve
```

CI also runs `gofmt -l .`, `go vet ./...`, and golangci-lint. Run them before you push.

## What to keep in mind

Privacy is the point of this service. A change must not store or publish anything the [Privacy](README.md#privacy) section says is never kept: prompts, responses, files, names, addresses, node IDs, or exact-hardware cohorts. A change to what is collected or published starts as an issue, before a pull request.

Schemas in `schema/` are shared with [toskar-model-data](https://github.com/yeixio/toskar-model-data) and Toskar Core. A change that isn't backward compatible needs a new schema version.

## Finding something to work on

Start with [good first issue](https://github.com/yeixio/toskar-ratings/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) or [help wanted](https://github.com/yeixio/toskar-ratings/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22). Comment on an issue if you'd like to take it.

Security problems go to [toskar-core's security policy](https://github.com/yeixio/toskar-core/blob/main/SECURITY.md), not a public issue.
