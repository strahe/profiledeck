# Update the Desktop App

Update support depends on how ProfileDeck was installed:

| Installation | Update method |
| --- | --- |
| macOS app or portable Linux Desktop | In-app update |
| Linux DEB or RPM | Install a newer package from [Releases](https://github.com/strahe/profiledeck/releases) |
| CLI built from source or local development Desktop | Rebuild or reinstall manually |

## In-app updates (macOS and portable Linux)

Stable receives stable releases; Beta receives Beta and stable releases. A fresh build follows its own channel, and later installs preserve the selected channel. Automatic checks are on by default and run at startup and about every six hours while ProfileDeck remains running. You choose when to restart after an update is ready.

Before restarting, ProfileDeck verifies the download and creates an encrypted application backup. If preparation fails, the current version stays installed. On portable Linux, the install directory must be writable; if ProfileDeck cannot safely replace the executable, use a newer portable archive manually.

## Update a Linux package

Download the matching new package from [ProfileDeck Releases](https://github.com/strahe/profiledeck/releases), then install it:

```bash
sudo apt install ./ProfileDeck_<version>_linux_amd64.deb
# or
sudo dnf install ./ProfileDeck_<version>_linux_amd64.rpm
```

DEB and RPM builds do not use in-app updates. Do not mix a package installation with the portable updater. Installation requirements are in [Getting Started](./getting-started.md).
