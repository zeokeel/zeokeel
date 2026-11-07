package contracts

import (
	"context"
	"io"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ACPService serves the Agent Client Protocol (newline-framed JSON-RPC 2.0)
// over a stream pair, driving the product's ChatService for editor clients
// (the old shell's ACP adapter).
type ACPService interface {
	// Serve runs the protocol loop until the input closes or ctx ends.
	Serve(ctx context.Context, in io.Reader, out io.Writer) error
}

// KeyACP is provided by the ACP backend module and started by the host when
// the process runs in ACP mode.
var KeyACP = cordis.NewServiceKey[ACPService]("zeokeel/acp")
