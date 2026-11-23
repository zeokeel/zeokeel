import { describe, expect, it, vi } from 'vitest';
import { EscArbiter, installEscListener } from './esc-arbiter';

function escKey(overrides: Partial<KeyboardEvent> = {}) {
  return {
    key: 'Escape',
    repeat: false,
    defaultPrevented: false,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
    ...overrides,
  } as unknown as KeyboardEvent;
}

describe('EscArbiter', () => {
  it('runs the strongest active claim and consumes the key', () => {
    const arbiter = new EscArbiter();
    const draft = vi.fn(() => true);
    const stopRun = vi.fn(() => true);
    // Both active while a run streams with text in the composer: the chain says
    // stopping the run (L5) outranks clearing the draft (L6).
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => true, onEscape: draft });
    arbiter.push({ id: 'stop', level: 'stopRun', active: () => true, onEscape: stopRun });

    const key = escKey();
    expect(arbiter.handleKey(key)).toBe(true);
    expect(stopRun).toHaveBeenCalledTimes(1);
    expect(draft).not.toHaveBeenCalled();
    expect(key.preventDefault).toHaveBeenCalledTimes(1);
    expect(key.stopPropagation).toHaveBeenCalledTimes(1);
  });

  it('keeps the documented order: approval outranks stopping a run (E3)', () => {
    const arbiter = new EscArbiter();
    const approval = vi.fn(() => true);
    const stopRun = vi.fn(() => true);
    // Pushed in the "wrong" order on purpose: the chain is by level, not by
    // mount order.
    arbiter.push({ id: 'stop', level: 'stopRun', active: () => true, onEscape: stopRun });
    arbiter.push({ id: 'approve', level: 'approval', active: () => true, onEscape: approval });

    arbiter.handleKey(escKey());
    expect(approval).toHaveBeenCalledTimes(1);
    expect(stopRun).not.toHaveBeenCalled();
  });

  it('lets a lower claim act when a higher one declines', () => {
    const arbiter = new EscArbiter();
    const declined = vi.fn(() => false);
    const acted = vi.fn(() => true);
    // clearDraft is the lowest level, so it only sees Esc when stopRun declines.
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => true, onEscape: acted });
    arbiter.push({ id: 'run', level: 'stopRun', active: () => true, onEscape: declined });

    expect(arbiter.handleKey(escKey())).toBe(true);
    expect(declined).toHaveBeenCalledTimes(1);
    expect(acted).toHaveBeenCalledTimes(1);
  });

  it('skips inactive claims — an empty draft does not claim Esc (E8)', () => {
    const arbiter = new EscArbiter();
    const onEscape = vi.fn(() => true);
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => false, onEscape });

    const key = escKey();
    expect(arbiter.handleKey(key)).toBe(false);
    expect(onEscape).not.toHaveBeenCalled();
    // Esc belongs to the browser: no preventDefault, so fullscreen exit etc.
    // still work.
    expect(key.preventDefault).not.toHaveBeenCalled();
  });

  it('ignores auto-repeat so holding Esc fires once (E9)', () => {
    const arbiter = new EscArbiter();
    const onEscape = vi.fn(() => true);
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => true, onEscape });

    const repeat = escKey({ repeat: true });
    expect(arbiter.handleKey(repeat)).toBe(false);
    expect(onEscape).not.toHaveBeenCalled();
    expect(repeat.preventDefault).not.toHaveBeenCalled();
  });

  it('ignores keys that are not Escape', () => {
    const arbiter = new EscArbiter();
    const onEscape = vi.fn(() => true);
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => true, onEscape });
    expect(arbiter.handleKey(escKey({ key: 'Enter' }))).toBe(false);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('prefers the most recently pushed claim within one level', () => {
    const arbiter = new EscArbiter();
    const first = vi.fn(() => true);
    const second = vi.fn(() => true);
    arbiter.push({ id: 'a', level: 'overlay', active: () => true, onEscape: first });
    arbiter.push({ id: 'b', level: 'overlay', active: () => true, onEscape: second });
    arbiter.handleKey(escKey());
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it('is indifferent to focus: a claim acts while focus is elsewhere (E1/E2 root cause)', () => {
    const arbiter = new EscArbiter();
    const deny = vi.fn(() => true);
    arbiter.push({ id: 'approval', level: 'approval', active: () => true, onEscape: deny });
    // No DOM, no focus: the arbiter only reads the claim stack.
    expect(arbiter.handleKey(escKey())).toBe(true);
    expect(deny).toHaveBeenCalledTimes(1);
  });
});

describe('installEscListener', () => {
  function fakeTarget() {
    const listeners: Array<{ type: string; fn: EventListener; capture: boolean }> = [];
    return {
      listeners,
      addEventListener: (type: string, fn: EventListener, options?: AddEventListenerOptions) => {
        listeners.push({ type, fn, capture: options?.capture ?? false });
      },
      removeEventListener: (type: string, fn: EventListener, options?: EventListenerOptions) => {
        const index = listeners.findIndex(
          (item) => item.type === type && item.fn === fn && item.capture === (options?.capture ?? false),
        );
        if (index >= 0) listeners.splice(index, 1);
      },
    } as unknown as Document & { listeners: typeof listeners };
  }

  it('installs exactly one capture-phase keydown listener, and removes it', () => {
    const target = fakeTarget();
    const uninstall = installEscListener(target, new EscArbiter());
    expect(target.listeners).toHaveLength(1);
    expect(target.listeners[0]?.type).toBe('keydown');
    expect(target.listeners[0]?.capture).toBe(true);
    uninstall();
    expect(target.listeners).toHaveLength(0);
  });

  it('routes a real Escape event through the claim stack', () => {
    const target = fakeTarget();
    const arbiter = new EscArbiter();
    const onEscape = vi.fn(() => true);
    arbiter.push({ id: 'draft', level: 'clearDraft', active: () => true, onEscape });
    const uninstall = installEscListener(target, arbiter);

    const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true });
    target.listeners[0]?.fn(event);
    expect(onEscape).toHaveBeenCalledTimes(1);
    uninstall();
  });
});