# Contributing

## Branching model (trunk-based development)

- `develop` is the trunk. All feature work branches off `develop` and merges back into it
  frequently via short-lived branches (aim for a lifetime of a few days, not weeks).
- `master` tracks what's actually released; `develop` is promoted to `master` periodically via PR.
- Branch names: `feature/<short-description>`, `fix/<short-description>`, `chore/<short-description>`.
- Both `master` and `develop` are protected: direct pushes are blocked, PRs require ≥1 approval
  and a passing CI run, and history is kept linear (no merge commits — squash or rebase your PR).

## Commit messages

This repo enforces [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) on
PR titles (checked by the `PR title lint` workflow), since squash-merge uses the PR title as
the final commit message on `develop`/`master`. Format:

```
<type>(<optional scope>): <description>
```

Common types: `feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `ci`, `build`. Example:

```
feat(api): add wallet address validation to the Shop CRD
```

Individual commits within a PR aren't linted — only the PR title needs to follow this format.

## Opening a PR

1. Branch off `develop`.
2. Commit your changes (any commit message style is fine locally).
3. Open a PR into `develop` with a Conventional Commits-style title.
4. Get at least one approval and a green CI run before merging.
5. Squash-merge (keeps history linear) — this repo's merge button is restricted to
   squash/rebase merges only.
