# Update the Desktop App

Desktop updates are available in signed Universal builds on macOS 14 or later. The CLI does not update itself, and local `dev` Desktop builds do not check for online updates or show the channel selector.

ProfileDeck offers two persisted update channels:

- **Stable** receives stable releases (`X.Y.Z`) only.
- **Beta** receives Beta releases (`X.Y.Z-beta.N`) and stable releases, so a Beta can move to the stable release for the same or a later version.

A fresh stable build starts on Stable, and a fresh Beta build starts on Beta. Later installs preserve your choice, including after a Beta updates to a stable release.

## Check for updates

Automatic checks are on by default. ProfileDeck checks after startup and every six hours while it remains open or hidden in the menu bar.

Open **Settings → General → App updates** to choose the update channel, turn automatic checks on or off, check now, or view download progress. ProfileDeck stays open while an update downloads. After an update is found, the sidebar also shows its download and preparation status.

You can change channels while the updater is idle, up to date, or showing an error. ProfileDeck waits until an active check, download, or pending restart finishes before allowing another channel change. When automatic checks are enabled, changing channels starts a new check immediately.

## Install a downloaded update

When the update is ready, choose **Restart to update** in the sidebar or **Restart now** in the prompt. Choose **Later** to keep working; the sidebar action remains available, and ProfileDeck never restarts without confirmation.

Before restarting, ProfileDeck verifies the update and creates an encrypted automatic application backup. If verification, backup, or preparation fails, the update is not installed and the current version remains in place. Update backups share the latest-ten automatic retention pool. Return to **Settings → General → App updates** and try again later.

## Understand the downloaded files

GitHub Releases provides a notarized Universal DMG for first installation and a notarized Universal ZIP for automatic updates. ProfileDeck downloads only the ZIP whose version matches the GitHub Release and verifies it against the published `SHA256SUMS` entry before preparing the update.

Release notes live on the corresponding [GitHub Release](https://github.com/strahe/profiledeck/releases).
