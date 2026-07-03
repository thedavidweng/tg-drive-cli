# Contributing

## Setup

```sh
make bootstrap
make ci-local
```

## Development

- Implement stages from `IMPLEMENTATION_PLAN.md` in order.
- Add tests with each behavior change.
- Update contracts when CLI, JSON, storage, or config behavior changes.

## Commit style

Use Conventional Commits:

```text
feat: add scan rebuild
fix: handle UTF-16 caption budget
docs: update storage contract
test: add slug collision cases
ci: update release workflow
```
