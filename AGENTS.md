# Ponder-Go workspace

- This repository is a local Go port of ponder-sh/ponder at 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334 (0.18.2).
- Scope is this repository and the sibling, pinned `../ponder-upstream` oracle. Do not modify other business repositories, push, create PRs, deploy, or connect business databases.
- Use new task-owned schemas/data directories only. PostgreSQL and PGlite remain distinct supported backends; never substitute SQLite.
- Treat `parity/test-ledger.json` discovery, porting, execution, pass/fail, and blockers independently. No fake skips or claims of full parity.
- Preserve original test source, nested names, parameters, inputs, assertions, skip/todo/fails states. Add exact mappings before marking a port complete.
- Keep business write, undo journal, event checkpoint, and progress in one transaction. Default multichain rollback undoes the global operation suffix; isolated rollback is chain-scoped.
- Run `go test ./...`, `go test -race ./...`, and focused upstream oracles as relevant. Record exact commands and results in `parity/runs/`.
