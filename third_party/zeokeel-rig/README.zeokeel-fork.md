# zeokeel-rig v0.16.3 — temporary in-tree fork

This directory is a verbatim copy of `/data/zeokeel-rig` tag `v0.16.3`
(commit `0515026`, based on v0.16.2 + the Windows sandbox concurrent-ACL fix).
It exists so the product keeps a reproducible clean checkout while the patched
rig release is not yet published to a module proxy. `go.mod` replaces the module
with this directory.

- Upstream source: `/data/zeokeel-rig` (tag `v0.16.3` is local/unpublished).
- Sync: `git -C /data/zeokeel-rig archive v0.16.3 | tar -x -C third_party/zeokeel-rig`.
- Removal: once rig v0.16.3 (or newer) is published, delete this directory,
  drop the `replace`, and bump the `require` version in `/data/mygo/go.mod`.
- Only the Windows sandbox ACL helper differs from v0.16.2; Linux/macOS behavior
  is unchanged.
