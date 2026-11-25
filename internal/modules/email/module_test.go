package email_test

// The module descriptor is part of the seam: assembly documents refer to it by
// ID and the pre-flight validator checks the contract graph, so both are pinned.

import (
	"context"
	"errors"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

func TestDescriptorMetadata(t *testing.T) {
	descriptor := email.Module()
	if descriptor.ID != email.ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, email.ModuleID)
	}
	if email.ModuleID != "zeokeel/email" {
		t.Fatalf("ModuleID = %q, want %q", email.ModuleID, "zeokeel/email")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyEmail.Name() {
		t.Fatalf("provides = %+v, want the email key", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
		if requirement.Optional {
			t.Fatalf("requirement %q is optional, want required", requirement.Name)
		}
	}
	if len(required) != 1 || !required[contracts.KeyInbox.Name()] {
		t.Fatalf("requires = %+v, want only the inbox", descriptor.Requires)
	}
}

func TestModuleActivatesAndProvidesTheService(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	inbox := newFakeInbox()
	if _, err := root.Provide(contracts.KeyInbox, contracts.InboxService(inbox)); err != nil {
		t.Fatalf("provide inbox: %v", err)
	}

	registry := module.NewRegistry()
	// The inbox is stood in at the root: this test is about the email module, not
	// about loading the inbox module (the assembly graph owns that).
	registry.AddBuiltin(contracts.KeyInbox)
	if err := registry.Register(email.Module()); err != nil {
		t.Fatalf("register email module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "email", Module: email.ModuleID}})
	if err != nil {
		t.Fatalf("load email module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})

	instance, ok := loader.Instance("email")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("email instance = (%v, %v), want active", ok, instance.State())
	}
	service, ok := root.Get(contracts.KeyEmail)
	if !ok || service == nil {
		t.Fatal("contracts.KeyEmail was not provided by the module")
	}

	// An unconfigured mailbox reports disconnected and refuses to send instead of
	// dialing anything.
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want disconnected for an empty configuration", status)
	}
	if err := service.Send(context.Background(), "owner@example.com", "subject", "body"); !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("Send on an unconfigured mailbox = %v, want ErrEmailUnauthorized", err)
	}
	if _, err := service.PollOnce(context.Background()); !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("PollOnce without an imap client = %v, want ErrEmailUnauthorized", err)
	}
}

func TestModuleFailsWithoutTheInbox(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})

	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyInbox)
	if err := registry.Register(email.Module()); err != nil {
		t.Fatalf("register email module: %v", err)
	}
	// The inbox key is declared as a builtin but nothing provides it: pre-flight
	// validation must reject the assembly rather than activate a broken module.
	if _, err := module.Load(root, registry, []module.Entry{{ID: "email", Module: email.ModuleID}}); err == nil {
		t.Fatal("Load without a provided inbox = nil error, want a validation failure")
	}
}
