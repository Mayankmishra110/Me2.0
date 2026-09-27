## Phase / feature
<!-- e.g. phase/p1-foundation → main -->

## Features in this PR
| Ticket | Role | Branch | Audit doc | Reviews |
|---|---|---|---|---|
| M2-XXX | be | m2/M2-XXX | [docs/audit/<phase>/M2-XXX.md](../docs/audit/) | QA pass · SEC pass |

## Commits
<!-- git log --oneline main..HEAD -->

## Checks (real output)
```
gofmt -l . / go vet ./... / go test ./...
web: npm run format:check && npm run lint && npm run typecheck && npm test && npm run build
media-tools: uv run ruff check . && uv run pytest
```

## Config and keys
<!-- new config keys / .env names; what works with which keys -->

## Risks and follow-ups

## How to test
