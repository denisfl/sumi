---
name: commits
description: Conventional Commits and branch naming conventions for Occultus Space.
applyTo: "**"
---

# Git Conventions — Occultus Space

## Commit Messages

Format: `<type>(<scope>): <description>`

### Types

| Type       | Description                          |
| ---------- | ------------------------------------ |
| `feat`     | New feature                          |
| `fix`      | Bug fix                              |
| `refactor` | Code refactoring (no feature change) |
| `chore`    | Build, deps, CI, tooling             |
| `docs`     | Documentation only                   |
| `style`    | Formatting (no code logic change)    |
| `test`     | Tests only                           |
| `perf`     | Performance improvement              |

### Scopes

| Scope      | Area                            |
| ---------- | ------------------------------- |
| `auth`     | Authentication / registration   |
| `chat`     | Chat screen, messages, timeline |
| `rooms`    | Room list, room management      |
| `matrix`   | matrix-js-sdk integration       |
| `ui`       | UI components, Tamagui          |
| `nav`      | Navigation                      |
| `store`    | Zustand stores                  |
| `i18n`     | Localization                    |
| `push`     | Push notifications              |
| `e2ee`     | Encryption                      |
| `media`    | Media upload/download           |
| `settings` | Settings screen                 |
| `deps`     | Dependencies                    |

### Examples

```
feat(auth): add email registration with referral field
fix(chat): fix timeline scroll position after pagination
refactor(store): migrate roomsStore to new Zustand API
chore(deps): update matrix-js-sdk to 34.x
docs: update architecture.md with navigation flow
```

## Branch Naming

Format: `<type>/<short-description>`

```
feat/auth-registration
feat/chat-timeline
fix/dm-encryption
refactor/store-migration
chore/expo-sdk-upgrade
```

## Pull Request

Title follows commit message format.  
Description includes:

- What changed
- Why
- Testing notes
- Screenshots (if UI)
