ZeoKeel for Linux
=================

ZeoKeel is a pure-Go desktop AI agent: the application itself builds with
CGO_ENABLED=0 and carries no webview or Node runtime. The binary is linked
against the system glibc by the graphics layer's purego/fakecgo shim, so it is
NOT a static Alpine/scratch binary: run it on a glibc-based distribution
(Debian/Ubuntu, Fedora, ...). It also loads the X11 libraries at runtime (see
Requirements); both of those are the package's runtime dependencies.

Install
-------

  tar -xzf zeokeel-linux-<arch>.tar.gz     # e.g. zeokeel-linux-amd64.tar.gz
  cd zeokeel-linux-<arch>                  # e.g. zeokeel-linux-amd64
  ./zeokeel

The archive directory is named zeokeel-linux-<arch> with no version number;
the release version is embedded in the binary and printed by
`./zeokeel --version`.

Requirements
------------

* Linux x86-64 or aarch64.
* glibc (the binary is dynamically linked; musl-only systems such as Alpine are
  not supported by this package).
* An X11 desktop session with the usual X11 client libraries (libX11 is loaded
  via dlopen). ZeoKeel runs natively (mygo) but renders through the platform
  graphics stack, so a headless machine needs Xvfb (`xvfb-run -a ./zeokeel`)
  for screenshots/tests.
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

Package checksums are in dist/SHA256SUMS.txt next to the archive (and as a
GitHub release asset); the file is not inside the tar.gz.

Unsigned package: this build is not GPG-signed. Verify the SHA-256 digest and
build from source (`make build`) if you need provenance.
