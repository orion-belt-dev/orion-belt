# Orion Belt docs

Source for [orion-belt.dev](https://orion-belt.dev), kept on the
`orion-belt-docs` branch of `orion-belt-dev/orion-belt`, next to the code.

| Branch | Role |
| --- | --- |
| `orion-belt-docs` (this branch) | Docusaurus source |
| `gh-pages` | Published static site |
| `master` | Product source |

## Develop

```bash
git switch orion-belt-docs   # or: git worktree add ../orion-belt-docs orion-belt-docs
nvm use 22                   # any Node >= 20
npm ci
npm start
```

## Publish to GitHub Pages

A push to `orion-belt-docs` deploys. `.github/workflows/deploy.yml` builds the
site and pushes **only the `build/` output** to `gh-pages` (custom domain
`orion-belt.dev`), authenticating with the workflow's own `GITHUB_TOKEN`.

Fallback when Actions is unavailable, from a local checkout of this branch:

```bash
nvm use 22
./scripts/deploy-gh-pages.sh
```

## Content

- `docs/`: unreleased "Next"; published cuts in `versioned_docs/` (default **1.3.0**)
- `blog/`: site blog
- `src/pages/`: marketing homepage
- `static/`: favicons, fonts and brand marks (`static/img/orion-belt-*`)
