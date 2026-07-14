# Desktop Updates

Desktop updates are available in macOS arm64 Alpha builds on macOS 14 or later. The CLI does not update itself.

## Check for updates

Automatic checks are enabled by default. ProfileDeck checks after startup and every six hours while it remains running. When an update is available, ProfileDeck downloads it in the background and stays open.

Open **Settings → App updates** to turn automatic checks on or off, check now, or view download progress.

## Install an update

When an update is ready, choose **Restart now** to install it or **Later** to keep working. ProfileDeck never restarts without confirmation.

Before installing, ProfileDeck backs up its local data. If the update cannot be verified or prepared, it is not installed and the current version remains in place. Return to Settings and try again later.

## Open an Alpha for the first time

Current Alpha builds are not notarized by Apple, so macOS may block the first launch. If you downloaded ProfileDeck from the [official ProfileDeck Releases page](https://github.com/strahe/profiledeck/releases) and trust the file, open **System Settings → Privacy & Security** and use **Open Anyway** for ProfileDeck.

This extra step applies only when opening a downloaded Alpha for the first time.
