// Package updater is ZeoKeel's auto-update engine (TDD scaffold, first round:
// RED tests only, no implementation).
//
// # Why this package exists
//
// ZeoKeel ships signed-less portable builds (zip/tar.gz/.app) today; users
// update by downloading a new archive by hand. AR-03 / PD-05 in docs/BUGS.md
// track that gap. This package is the single place that will own:
//
//  1. Check   – fetch a release manifest, compare versions, pick the asset for
//     the running GOOS/GOARCH.
//  2. Download – stream the asset to a staging directory and verify its
//     SHA-256 before anything is executed.
//  3. Stage   – remember a verified update as "pending".
//  4. Install – on the next start (install-on-restart), replace the running
//     executable through a platform Replacer, then clean the staging area.
//
// # Contract (tests in updater_test.go are the executable form)
//
// Manifest (served as JSON from Config.FeedURL):
//
//	{
//	  "version": "1.2.0",
//	  "notes": "release notes",
//	  "assets": [
//	    {"goos":"linux","goarch":"amd64","url":"https://…/zeokeel-linux-amd64",
//	     "sha256":"<hex>","size":123456}
//	  ]
//	}
//
// Security invariants (all fail closed):
//   - Feed and asset URLs must be https unless Config.AllowInsecureHTTP is set
//     (tests only). Empty FeedURL => ErrDisabled, not a silent no-op error.
//   - The downloaded bytes must match the manifest's SHA-256 before they are
//     staged => ErrChecksumMismatch, and no partial file is left behind.
//   - Staged/pending paths must stay inside <DataDir>/updates
//     (path traversal is rejected).
//   - InstallOnRestart only replaces the executable through the injected
//     Replacer, so the logic is testable without touching the real binary; a
//     failed replace keeps the pending update for the next start instead of
//     half-writing the binary.
//   - Manifests without a matching GOOS/GOARCH asset => ErrNoAsset.
//
// Version comparison is a pragmatic semver: MAJOR.MINOR.PATCH with an optional
// "-prerelease"; a prerelease sorts before its release (1.2.0-rc.1 < 1.2.0);
// builds with missing components are treated as 0 (1.2 == 1.2.0).
package updater
