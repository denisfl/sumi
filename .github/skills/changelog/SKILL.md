---
name: changelog
description: Changelog format and conventions for Occultus Space.
applyTo: "CHANGELOG.md"
---

# Changelog Skill — Occultus Space

## Format

Based on [Keep a Changelog](https://keepachangelog.com/) + semver.

```markdown
# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- New feature description

### Changed

- Modified behavior description

### Fixed

- Bug fix description

### Removed

- Removed feature description

## [1.0.0] - 2026-XX-XX

### Added

- Initial release: auth, DM, group chats, E2EE, push
```

## Categories

| Category     | Use for                           |
| ------------ | --------------------------------- |
| `Added`      | New features                      |
| `Changed`    | Changes in existing functionality |
| `Deprecated` | Soon-to-be removed features       |
| `Removed`    | Removed features                  |
| `Fixed`      | Bug fixes                         |
| `Security`   | Security-related changes          |

## Rules

- Each entry is a single line starting with `- `
- Use imperative mood: "Add feature" not "Added feature"
- Reference issue/PR numbers when available: `(#123)`
- Group by category within each version
- `[Unreleased]` section always at top
- Date format: `YYYY-MM-DD`
