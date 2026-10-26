# Publish BuildHeadway's terminal package

The installable package is `buildheadway`; it exposes the `buildheadway` command. The project is not published to npm or a public Git repository by the local build process. Do not advertise a registry installation command until publication succeeds.

## Verify and build

Use Go, Node.js 20+ with npm, and Python 3. Run from the source directory:

```sh
go test -buildvcs=false -race ./...
go vet -buildvcs=false ./...
python3 release.py --output dist --repository OWNER/REPOSITORY
```

`OWNER/REPOSITORY` must be the actual chosen repository. Omit `--repository` for an offline candidate with no public update source.

The build creates `dist/buildheadway-0.19.1.tgz` and `dist/buildheadway.zip` after the npm integration checks pass. The npm package is the user installation artifact. The ZIP contains the source, native builds, standalone terminal installers, license notices, and validation details.

The included native CI must run on Windows, macOS, and Linux before describing the release as verified on all three. Cross-compilation does not count as native execution. This candidate is unsigned.

## Publish using the maintainer's npm account

Confirm ownership or availability of the package name, choose the intended source repository, and configure that repository in the npm metadata before publishing. If the name is unavailable, update the name in `npm/package.json`, the help messages, documentation, and package tests while keeping the user command `buildheadway`.

Once the package has passed review, the maintainer can run:

```sh
npm login
npm publish ./dist/buildheadway-0.19.1.tgz --access public
```

Publication requires the maintainer's account and any npm verification. Credentials are not included in this project. Prefer npm trusted publishing when connecting a repository release pipeline.

After a successful publication, the normal installation command is:

```sh
npm install --global buildheadway
```

Then users run `buildheadway` in their project. Until publication succeeds, the exact same package can be installed from its downloaded `.tgz` file with npm, as shown in README.md.

## Native release assets

The build also creates `dist/release-assets/` with six executables, `SHA256SUMS`, `install.sh` and `install.ps1`. Upload these as assets of the matching `v0.19.1` release. Keep every asset in one release; installers and updates resolve a version before downloading.

The native verification workflow builds on Windows, macOS and Linux and retains the artifacts. The separate release workflow creates a **draft** release only when manually dispatched with a matching version tag, after its own three-OS native verification passes. Review and publish that draft before advertising internet install/update commands. npm publication remains a separate account-authorized action.

The selected product name is BuildHeadway and the source repository is `agentic-dev8k/buildheadway`. Keep that identity consistent in release metadata and artwork; see NAMING.md and BRANDING.md.
