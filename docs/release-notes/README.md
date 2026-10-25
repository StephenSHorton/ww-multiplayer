# Release notes

One file per release, `v<version>.md`, player-facing Markdown. The release workflow (`.github/workflows/release.yml`) publishes the tag's file as the GitHub release's body, and GitHub appends its generated "What's Changed" list and the full-changelog link, so a file holds neither. `scripts/release-notes.sh <tag>` picks the file: `<tag>.md`, or for a prerelease tag without its own (`v0.6.0-rc1`) its version's (`v0.6.0.md`).

The files for v0.1.0 to v0.5.0 are the published bodies as they were (`gh release view <tag>`), without the generated part.

## Cutting a release

1. Write `v<version>.md`, starting with `## Wind Waker Multiplayer v<version>`. The newest file is the model: who it is for, what is new, what a player needs, the version rule, upgrading, then the Windows, macOS, co-op and command-line steps.
2. A file being drafted on `main` starts with a `<!-- draft: ... -->` line saying what is left. The workflow refuses a draft: delete that line when the notes are done.
3. Merge it to `main`, then push the tag. A tag with no notes file, or only a draft, fails in the workflow's first job, before anything is built or published.
