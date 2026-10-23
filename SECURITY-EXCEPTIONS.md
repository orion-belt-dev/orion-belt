# Security exceptions

Findings left in place, with a review date. Scanner ignore files are not used.

## GHSA-vfj7-8cjw-p6xm

- Package: `braces@3.0.3` (via `micromatch`/`fast-glob`/`chokidar` in Docusaurus's build and dev server)
- Advisory: [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm), stack exhaustion on deeply nested brace patterns
- Also: CVE-2026-93687. `npm audit` reports it as 29 high findings, one per package that depends on `braces` (`micromatch`, `fast-glob`, `globby`, `webpack-dev-server`, the `@docusaurus/*` packages). They are all this one advisory.
- `npm audit fix` changes nothing; `--force` would move Docusaurus to a canary or older major. Rechecked 2026-10-22: no patched `braces` release, `micromatch@4.0.8` and `@docusaurus/core@3.10.2` are the latest.
- Why: no fixed release exists (every version up to 3.0.3 is affected). It runs only at build time and in the local dev server, on glob patterns from this repo's own config, never on visitor input, and it is not part of the published static site.
- Owner: mdarguechi
- Review by: 2027-01-20
