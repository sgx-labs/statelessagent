---
title: "Release Process"
domain: devops
tags: [release, deploy, versioning, process]
confidence: 0.85
content_type: process
---

# Release Process

## Version Scheme

Semantic versioning: `MAJOR.MINOR.PATCH`
- **MAJOR**: Breaking API changes
- **MINOR**: New features, backward compatible
- **PATCH**: Bug fixes, no feature changes

## Release Steps

### 1. Prepare Release

```bash
# Update version in version.go
# Update CHANGELOG.md with release notes
git checkout -b release/v0.12.0
```

### 2. Create Release PR

- Title: `release: v0.12.0`
- Body: Changelog entries for this release
- Must pass all CI checks (lint, test, build)
- Requires 1 approval

### 3. Tag and Build

```bash
git tag v0.12.0
git push origin v0.12.0
# GitHub Actions builds and publishes binaries
```

### 4. GitHub Release

- Create release from tag on GitHub
- Attach compiled binaries for Linux, macOS, Windows
- Copy changelog entries into release notes

### 5. Distribution

- Binary uploaded to GitHub Releases
- npm package updated (`npx same`)
- Homebrew formula updated (tap repo)
- Docker image pushed to registry

## Hotfix Process

For critical bugs in production:

1. Branch from the release tag: `hotfix/v0.12.1`
2. Apply minimal fix + test
3. Fast-track review (1 reviewer, expedited)
4. Tag and release as patch version
5. Cherry-pick fix back to main

## Rollback

If a release has critical issues:
1. Deploy previous version from GitHub Releases
2. Run `same reindex` if schema changed (down migration first)
3. Post incident review within 24 hours
