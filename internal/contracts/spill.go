package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
)

// KeySpillStore is the toolkit-provided store for oversized tool results. The
// agent puts it on the run's SpillPolicy (so results become handles instead of
// files) and the toolkit registers spill_read against the same store, so the
// model can read a spilled result back. Optional: a stripped assembly keeps the
// file-backed spill path.
var KeySpillStore = cordis.NewServiceKey[rig.SpillStore]("zeokeel/spill-store")
