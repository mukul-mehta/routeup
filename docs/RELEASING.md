# Releasing routeup

Releases are immutable and start from a clean `main` checkout. Never move or
reuse a published tag; cut a patch release when a release needs correction.

## Prepare

```bash
git fetch origin --tags
git status --short
git switch main
git pull --ff-only
just ci
fly config validate -c deploy/fly.toml
fly config validate -c deploy/log-shipper/fly.toml
goreleaser check
goreleaser release --snapshot --clean
git diff --exit-code
```

Review user-visible changes and choose a semantic version:

```bash
VERSION=v0.8.0
git log --oneline "$(git describe --tags --abbrev=0)..HEAD"
```

## Publish

```bash
git tag -a "$VERSION" -m "$VERSION"
git push origin "$VERSION"
```

The `Release` GitHub Actions workflow validates the tag, modules, tests,
installer, and GoReleaser configuration before publishing four archives and
`checksums.txt`, then updates the Homebrew tap.

Watch the workflow:

```bash
gh run list --workflow release.yml --limit 1
gh run watch
```

## Verify

```bash
WORK=$(mktemp -d)
gh release download "$VERSION" --dir "$WORK"
(cd "$WORK" && shasum -a 256 -c checksums.txt)

mkdir -p "$WORK/home"
HOME="$WORK/home" ROUTEUP_INSTALL_DIR="$WORK/bin" sh install.sh
"$WORK/bin/routeup" --version

brew update
brew fetch --cask mukul-mehta/tap/routeup
```

Run a real route smoke test with the released binary. On macOS, the safe
high-port harness avoids modifying trust or privileged bind state:

```bash
bash scripts/integration-macos.sh "$WORK/bin/routeup"
```

Verify the GitHub release contains Darwin and Linux archives for amd64 and
arm64, the Homebrew cask has the new version and checksums, the curl installer
downloads successfully, and `routeup update --check` reports the release.

## Roll Back

Do not replace release assets. If the release is broken, document the issue,
restore server deployment separately if needed, fix `main`, and publish the next
patch version. Users can install an earlier Homebrew cask revision or download a
specific GitHub release while the patch is prepared.
