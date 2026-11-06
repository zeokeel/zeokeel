ZeoKeel for macOS
=================

ZeoKeel is a pure-Go desktop AI agent. This package is an unsigned `.app`
bundle built for macOS 13 or newer (arm64 and amd64 variants).

Install
-------

  unzip zeokeel-macos-arm64.zip
  mv ZeoKeel.app /Applications/
  open /Applications/ZeoKeel.app

First launch of an unsigned app
-------------------------------

The bundle is **not code-signed or notarized** (signing requires an Apple
Developer certificate and a release pipeline). macOS Gatekeeper will refuse the
first launch; to allow it:

  xattr -dr com.apple.quarantine /Applications/ZeoKeel.app
  open /Applications/ZeoKeel.app

or: System Settings > Privacy & Security > "Open Anyway".

Data and logs
-------------

  ~/Library/... is not used: data lives in ~/.zeokeel (override with ZEOKEEL_DATA_DIR),
  ~/.zeokeel/logs/app-YYYYMMDD.log and crash-*.txt as on Linux.

Requirements
------------

* macOS 13+ on Apple Silicon or Intel.
* A model provider API key via `DEEPSEEK_API_KEY` or Settings.

Package checksums are in SHA256SUMS.txt next to the archive.

Signing/notarization is tracked in docs/BUGS.md (AR-03) and is deliberately
deferred: it needs a Developer ID certificate and CI secrets, which this
repository does not have.
