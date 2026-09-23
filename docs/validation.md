# Validation scope / 验证范围

The first release is gated by `go test ./...`, `go vet ./...`, Linux race checks, six cross-compilation targets and Playwright against a prebuilt binary and synthetic demo.

Accounting fixtures contain only synthetic or sanitized metering metadata. They exercise copied content blocks, Windows/WSL mirrors, missing/late request identity, partial/final usage, compaction and Advisor iteration models, cache lifetimes, future models, explicit Fast, third-party filtering and subagent relationships. Reliability checks include malformed/large records, partial tails, deleted/rewritten files, persistent rebuild approval, repeat scans and timezone/day boundaries. Update tests replace real executables and reopen the same database without installing startup entries.

Before release, native Windows and Ubuntu logs were reconciled in a dedicated ignored state directory. An independent JSONL calculation matched SQLite, API totals and exports; source-union queries counted mirrored requests only once. A second scan inserted zero events. No raw personal transcripts, database, identities, paths, titles or timestamps are committed.

Playwright covers overview, calendar/hour drilldown, details/search/task trees, minute ranges, source settings and union filters, JSON/CSV export, pricing, update choices/recovery, English/Chinese, keyboard access, desktop/mobile light/dark themes and reduced motion. Published screenshots and the static demo are synthetic, not real-user statistics.

| Target | Validation |
| --- | --- |
| Windows amd64 | Native tests, real-log reconciliation, isolated server and released-binary smoke test |
| Linux amd64 | Ubuntu/WSL native isolated scan; Linux CI tests and race detector |
| macOS arm64 | macOS CI tests plus install, restart and uninstall with a disposable LaunchAgent |
| macOS amd64 | Intel macOS CI tests plus the same service lifecycle checks |
| Windows arm64 | Cross-compilation; no native arm64 Windows execution claimed |
| Linux arm64 | Cross-compilation; no native arm64 Linux execution claimed |

CI and release run results remain the authoritative evidence for each commit. Release verification downloads all six binaries and `SHA256SUMS`, compares every hash and checks Windows version, isolated startup and update lookup. Local validation does not install claude-usage or enable startup on the development computer.
