# Update the Desktop App

How you update depends on how you installed ProfileDeck:

- **macOS app** or **portable Linux Desktop:** ProfileDeck can check for updates in the app.
- **Linux DEB or RPM:** download and install a newer package from GitHub Releases. The app does not check for or download in-app updates.
- **CLI** and **local `dev` Desktop builds:** no online update checks and no channel selector.

## In-app updates (macOS and portable Linux)

ProfileDeck offers two persisted update channels:

- **Stable** receives stable releases (`X.Y.Z`) only.
- **Beta** receives Beta releases (`X.Y.Z-beta.N`) and stable releases, so a Beta can move to the stable release for the same or a later version.

A fresh stable build starts on Stable, and a fresh Beta build starts on Beta. Later installs preserve your choice, including after a Beta updates to a stable release.

### Check for updates

Automatic checks are on by default. ProfileDeck checks after startup and every six hours while it remains open or stays running in the background (for example in the tray).

Open **Settings → General → App updates** to choose the update channel, turn automatic checks on or off, check now, or view download progress. ProfileDeck stays open while an update downloads. After an update is found, the sidebar also shows its download and preparation status.

You can change channels while the updater is idle, up to date, or showing an error. ProfileDeck waits until an active check, download, or pending restart finishes before allowing another channel change. When automatic checks are enabled, changing channels starts a new check immediately.

### Install a downloaded update

When the update is ready, choose **Restart to update** in the lower-left sidebar when you are ready. ProfileDeck stays open until you choose this action.

Before restarting, ProfileDeck verifies the update and creates an encrypted automatic application backup. If verification, backup, or preparation fails, the update is not installed and the current version remains in place. Update backups share the latest-ten automatic retention pool. Return to **Settings → General → App updates** and try again later.

On portable Linux, ProfileDeck also checks that it can safely move the downloaded update into the executable's directory before creating the backup or restarting. If the installation cannot be replaced safely, ProfileDeck keeps the current version and disables in-app updates for that installation. Download a newer portable release manually instead. See [Portable Desktop](./getting-started.md#portable-desktop).

## Update a Linux package

DEB and RPM installations do not use in-app updates. **Settings → General → App updates** explains that newer packages must be installed from GitHub Releases.

Download the matching newer package from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases), then install it:

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# or
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

Do not combine a DEB or RPM installation with the portable in-app updater. If a release has a problem, install a later fixed version when available.

See [DEB or RPM package](./getting-started.md#deb-or-rpm-package).

## Understand the downloaded files

GitHub Releases provides:

- a notarized Universal DMG for macOS installation
- a notarized Universal ZIP for macOS in-app updates
- a signed single-file tar archive for portable Linux Desktop installs and updates
- DEB and RPM packages for Linux Desktop and CLI installation

When the app updates itself, it downloads only the platform artifact selected by the signed Wails update manifest, then verifies its SHA-512 digest and Ed25519ph signature before preparing the update.

Release notes live on the corresponding [GitHub Release](https://github.com/strahe/profiledeck/releases).
