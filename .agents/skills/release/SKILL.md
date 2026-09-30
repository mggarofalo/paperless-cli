---
name: release
description: Release paperless-cli from protected main using a stable version tag, GitHub Actions, and verified platform archives. Use when asked to cut, publish, or verify a release of this repository.
---

# Release paperless-cli

Accept an explicit version (`0.1.0` or `v0.1.0`) or a `major`, `minor`, or `patch` bump. Default to patch only when the user did not specify a version. Stable versions use `vMAJOR.MINOR.PATCH`; prereleases need a workflow change first.

## Prepare the release commit

- Read root `AGENTS.md`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, and `.goreleaser.yaml`.
- Finish requested changes on a branch, open a PR, and wait for all checks, including **Release packaging** and the three `test (...)` jobs. Merge through normal branch protection; do not push to main or bypass checks.
- Use the existing user authorization for PR creation, merge, and publication. A request to release a specific version authorizes its tag and publication; do not ask for the same approval again. If the request was only to prepare a release, stop before publishing and present the concrete version and notes.
- Use a clean checkout of main (an isolated checkout is fine when unrelated user edits exist). Fetch origin and tags. Require `HEAD` to equal `origin/main`; fast-forward clean main as needed. Never discard unrelated edits.
- Wait for successful CI on that exact main commit. No private instance data or credentials belong in release notes, test fixtures, or the repository.

## Choose version and notes

Inspect local and remote tags plus `gh release list`. Determine the latest stable semantic version numerically, not lexicographically. If no release exists, use `0.0.0` as the bump base. Honor an explicit version exactly; it must be newer than any existing stable release.

Verify the target tag and release do not already exist. If they do, inspect their commit and publication state instead of overwriting them. Do not move, delete, or force-push a published tag.

Read commits since the previous release (all commits for the first release). Summarize user-facing features and fixes, breaking changes, and relevant build changes. GoReleaser generates grouped notes from commit subjects. For curated notes, edit the published release body using `gh release edit --notes-file` after publication; use a temporary file outside the repository to preserve literal text and newlines.

## Tag and publish

Create an annotated tag on the checked main commit:

```sh
git tag -a vMAJOR.MINOR.PATCH -m "Release vMAJOR.MINOR.PATCH"
git push origin refs/tags/vMAJOR.MINOR.PATCH
```

Push using the authenticated user's git credentials. The release pipeline triggers on a **tag push**, not a release-created event or a main push. A tag pushed by another workflow's default `GITHUB_TOKEN` does not trigger this pipeline; use an authorized user/App token if automating tag creation later.

The release job rejects malformed stable versions and commits outside main. GoReleaser v2.18.2 builds Linux, macOS, and Windows for amd64 and arm64, injects `main.version`, packages the binaries, uploads checksums, and publishes the GitHub release. It runs on macOS with CGO for native Keychain support; retain that when modifying builds.

## Verify completion

Find the run with `gh run list --workflow release.yml --branch vMAJOR.MINOR.PATCH --event push`; verify its head SHA matches the tagged commit. Monitor until it finishes successfully. A queued workflow is not a completed release.

If the run fails, inspect logs and fix the cause through a PR. Re-run the original job only for a transient failure with unchanged code, after inspecting any partial draft/assets. If code changes are needed after tagging, do not silently move the tag: explain the situation and resolve the version choice with the user.

Use `gh release view vMAJOR.MINOR.PATCH --json url,isDraft,isPrerelease,assets` to verify publication. Expect six archives named `paperless_VERSION_OS_ARCH.tar.gz` (Linux/macOS) or `.zip` (Windows), plus `checksums.txt`.

Download all seven assets into a temporary directory and run:

```sh
python scripts/verify_release.py TEMP_DIRECTORY --version MAJOR.MINOR.PATCH
```

This verifies SHA-256 hashes, archive contents, and the native binary's `--version`. Report the release URL, PR, version, and verification outcome. Never claim release completion solely because the tag exists.
