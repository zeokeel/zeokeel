ZeoKeel for Linux
=================

ZeoKeel is a pure-Go desktop AI agent. This package is a single static binary
with no runtime dependencies (no cgo, no webview, no Node).

Install
-------

  tar -xzf zeokeel-linux-amd64.tar.gz
  cd zeokeel-<version>-linux-amd64
  ./zeokeel

Requirements
------------

* Linux x86-64 or aarch64.
* An X11 desktop session. ZeoKeel runs natively (mygo) but renders through the
  platform graphics stack, so a headless machine needs Xvfb (`xvfb-run -a
  ./zeokeel`) for screenshots/tests.
* A model provider API key: set `DEEPSEEK_API_KEY` in the environment or add a
  provider in Settings. The key is stored in the app data directory, never in
  this package.

Data and logs
-------------

  ~/.zeokeel/                    default data directory (override with ZEOKEEL_DATA_DIR)
  ~/.zeokeel/index.sqlite        sessions, messages, events, settings
  ~/.zeokeel/sessions/<id>/      per-session JSONL transcript and artifacts
  ~/.zeokeel/logs/app-YYYYMMDD.log   application log (rotated at 8 MiB)
  ~/.zeokeel/logs/crash-*.txt    crash report written after a panic

Report a bug with the output of `./zeokeel --version` and the newest files
under `~/.zeokeel/logs/`.

Package checksums are in SHA256SUMS.txt next to the archive.

Unsigned package: this build is not GPG-signed. Verify the SHA-256 digest and
build from source (`make build`) if you need provenance.
