package email_test

// Shared fixtures: an in-memory inbox and a fake IMAP client. The email adapter
// is judged by what it puts in the inbox, so the fake inbox reproduces the one
// behaviour that matters: Add merges on the dedupe key and counts arrivals.

import (
	"context"
	"fmt"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

// fakeInbox is an in-memory contracts.InboxService.
type fakeInbox struct {
	mu     sync.Mutex
	items  []contracts.InboxItem
	index  map[string]int
	adds   int
	addErr error
}

func newFakeInbox() *fakeInbox {
	return &fakeInbox{index: make(map[string]int)}
}

func (f *fakeInbox) setAddError(err error) {
	f.mu.Lock()
	f.addErr = err
	f.mu.Unlock()
}

func (f *fakeInbox) Add(ctx context.Context, item contracts.InboxItem) (contracts.InboxItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxItem{}, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	if f.addErr != nil {
		return contracts.InboxItem{}, false, f.addErr
	}
	if index, ok := f.index[item.DedupKey]; ok {
		f.items[index].Count++
		return f.items[index], true, nil
	}
	item.ID = fmt.Sprintf("item-%d", len(f.items)+1)
	item.Count = 1
	item.Status = contracts.InboxStatusNew
	f.items = append(f.items, item)
	f.index[item.DedupKey] = len(f.items) - 1
	return item, false, nil
}

func (f *fakeInbox) List(_ context.Context, _ contracts.InboxFilter) ([]contracts.InboxItem, error) {
	return f.snapshot(), nil
}

func (f *fakeInbox) Get(_ context.Context, id string) (contracts.InboxItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return contracts.InboxItem{}, contracts.ErrInboxUnknownItem
}

func (f *fakeInbox) Triage(_ context.Context, _ []string, action string, _ []string) (int, error) {
	if action == "" {
		return 0, contracts.ErrInboxUnknownAction
	}
	return 0, nil
}

func (f *fakeInbox) Stats(_ context.Context) (contracts.InboxStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return contracts.InboxStats{Total: len(f.items), New: len(f.items)}, nil
}

// snapshot copies the stored items so a test can assert without holding the lock.
func (f *fakeInbox) snapshot() []contracts.InboxItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contracts.InboxItem, len(f.items))
	copy(out, f.items)
	return out
}

// addCount reports how many Add calls reached the inbox.
func (f *fakeInbox) addCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adds
}

var _ contracts.InboxService = (*fakeInbox)(nil)

// fakeIMAPClient is the injected inbound side: it hands back whatever messages
// the test queued, or the configured error.
type fakeIMAPClient struct {
	mu       sync.Mutex
	messages []email.RawMessage
	err      error
	calls    int
}

func (f *fakeIMAPClient) Unseen(ctx context.Context) ([]email.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]email.RawMessage, len(f.messages))
	copy(out, f.messages)
	return out, nil
}

func (f *fakeIMAPClient) setMessages(messages ...email.RawMessage) {
	f.mu.Lock()
	f.messages = messages
	f.mu.Unlock()
}

func (f *fakeIMAPClient) setError(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *fakeIMAPClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ email.IMAPClient = (*fakeIMAPClient)(nil)

// rawMail builds one message as an IMAP client would deliver it.
func rawMail(messageID, from, subject, body, encoding string) email.RawMessage {
	header := map[string][]string{
		"From":    {from},
		"Subject": {subject},
	}
	if messageID != "" {
		header["Message-ID"] = []string{messageID}
	}
	return email.RawMessage{Header: header, Body: []byte(body), Encoding: encoding}
}
