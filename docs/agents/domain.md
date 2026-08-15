# Domain docs

## Layout

Single-context: one domain glossary at the repo root and architectural decisions under `docs/adr/`.

- `CONTEXT.md` — domain glossary and ubiquitous language for `tg-drive-cli`
- `docs/adr/NNNN-slug.md` — architectural decision records

## Consumer rules

- Read `CONTEXT.md` before implementing any new feature or command. It defines the core terms used in `PRODUCT_SPEC.md`, contracts, and the code.
- Read `docs/adr/` when changing or extending an existing decision. The newest ADR may supersede earlier ones.
- Add a new ADR when introducing a new dependency, abstraction, or a change to the public JSON/exit-code contract.
- If a term is missing from `CONTEXT.md`, add it there before using it in code or docs.
