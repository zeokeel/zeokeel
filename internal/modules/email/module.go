package email

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/email"

// Module is the email adapter descriptor: it requires the inbox (its only
// destination) and provides contracts.KeyEmail. Credentials and the two server
// addresses arrive through configuration; without them the adapter reports
// unauthorized instead of pretending to be connected, and PollOnce refuses to
// guess at a missing IMAP client.
//
// The descriptor only wires the service. The poll loop is started by whoever owns
// the schedule (Adapter.Run), so loading the module never touches the network.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		adapter := NewAdapter(inboxService, cfg)
		if _, err := ctx.Provide(contracts.KeyEmail, contracts.EmailService(adapter)); err != nil {
			return fmt.Errorf("%s: provide email: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyEmail)).
		AddRequires(module.Requires(contracts.KeyInbox)).
		WithName("ZeoKeel email (SMTP/IMAP) adapter")
}
