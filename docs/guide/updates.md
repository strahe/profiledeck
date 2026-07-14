# Update the Desktop App

Desktop updates are available in distributed macOS arm64 Alpha builds on macOS 14 or later. The CLI does not update itself.

## Check for updates

Automatic checks are on by default. ProfileDeck checks after startup and every six hours while it remains open or hidden in the menu bar.

Open **Settings → App updates** to turn automatic checks on or off, check now, or view download progress. ProfileDeck stays open while an update downloads.

## Install a downloaded update

When the update is ready, choose **Restart now** to install it. Choose **Later** to keep working; ProfileDeck never restarts without confirmation.

Before restarting, ProfileDeck verifies the update and backs up its local data. If verification, backup, or preparation fails, the update is not installed and the current version remains in place. Return to **Settings → App updates** and try again later.

## Open an Alpha for the first time

Current Alpha builds are not notarized by Apple, so macOS may block the first launch. If you downloaded ProfileDeck from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases) and trust the file, open **System Settings → Privacy & Security** and choose **Open Anyway** for ProfileDeck.

This extra step applies only to the first launch of a downloaded Alpha.
