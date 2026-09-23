# Third-party notices

This project derives from **codex-usage**, copyright (c) 2026 zJay26,
licensed under MIT, at commit `e0ca1f916097aff4a12f73e54152f9480655c9e6`:
https://github.com/zJay26/codex-usage

The original MIT notice is retained in LICENSE. Claude-specific parsing,
accounting, pricing, source discovery and branding are independent adaptations.

`claude-usage` directly depends on:

- `modernc.org/sqlite` — BSD-3-Clause
- `golang.org/x/sys` — BSD-3-Clause

Transitive dependency licenses are preserved by their upstream modules. Run
`go list -m all` against the locked `go.sum` to inspect the exact dependency
graph used for a build.
