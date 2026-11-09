# Releases, code signing and the website

[← Developer setup](dev-setup.md) · [Docs index](README.md) · [Debug commands →](debug-commands.md)

How a release is built and published, and what signing does.

Pushing a `v*` tag runs `.github/workflows/release.yml`: it builds
`ww-multiplayer.exe` (Windows, cross-built on Linux) and
`ww-multiplayer-macos.zip` (`scripts/build-mac.sh` on macOS: a universal
`WW Multiplayer.app` with an `Info.plist` carrying the tag's version,
the icon, and `LSMinimumSystemVersion` 12.0, zipped with `ditto` so the
bundle and its signature survive), writes `SHA256SUMS` over both, and
publishes one GitHub Release (a tag with a `-` is a prerelease). The
release's body is `docs/release-notes/<tag>.md` with GitHub's generated
"What's Changed" list after it; write and merge that file before tagging
(a tag without one, or with a draft, fails before anything is built;
`docs/release-notes/README.md`). `tools/check_game_files` runs on the
repo and on every asset before it is published, and fails the release
if any holds a Nintendo game file or a save: by name, by header, and by
scanning each file whole (the exe and the app binary in the macOS zip
too) for an archive, compressed data, a model, a disc or save header
inside it. Running
the workflow by hand (workflow_dispatch) is a dry run: it builds both and
keeps them as workflow artifacts, but publishes nothing. There is no CI
on PRs or pushes: run the checks in `docs/TESTING.md` locally before
pushing (on a Mac, `./scripts/build-mac.sh local` is the packaging smoke test).

The Windows exe embeds `cmd/ww-multiplayer/ww_windows.syso`: the icon (`docs/img/icon.ico`)
and an application manifest (`packaging/ww-multiplayer.exe.manifest`:
per-monitor DPI awareness, Common Controls 6, `asInvoker`). It stays a
console-subsystem program, so one exe is the app, the terminal menu and
the CLI. After changing the icon or the manifest, run `go generate ./cmd/ww-multiplayer`
with [rsrc](https://github.com/akavel/rsrc) on `PATH`; that also rebuilds
the macOS icon `docs/img/icon.icns` (`tools/mkicns`).

**Code signing is optional and off for now.** Each signing step in
release.yml runs only when its repository secrets exist, so a fork (or
this repo today) builds the unsigned assets with nothing failing:

| Secrets | What they turn on |
|---|---|
| `WINDOWS_CERTIFICATE_PFX` (base64 .pfx), `WINDOWS_CERTIFICATE_PASSWORD` | Authenticode-signs the exe (osslsigncode, signtool's Linux equivalent, with an RFC 3161 timestamp) |
| `MACOS_CERTIFICATE_P12` (base64 .p12), `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGN_IDENTITY` | Signs the app with a Developer ID identity, hardened runtime and the cs.debugger entitlement, instead of ad-hoc |
| the three above plus `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` | Also notarizes the zip and staples the app |

Without them, users see the SmartScreen and Gatekeeper prompts described
in [Quick start](getting-started.md). macOS players need Prepare
Dolphin either way: Dolphin's own hardened runtime is what blocks
`task_for_pid`.

**The website** (`site/`, GitHub Pages) describes the latest release, so
`.github/workflows/pages.yml` deploys it when a release is published
(release.yml starts it after publishing a non-prerelease; a release
published by hand triggers it too), always from the latest release's
tag. A `site/` change merged to `main` goes live with the next release;
run the Pages workflow by hand, optionally with a `ref`, to redeploy
sooner.

---

[← Developer setup](dev-setup.md) · [Docs index](README.md) · [Debug commands →](debug-commands.md)
