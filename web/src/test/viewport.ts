/**
 * Narrow-viewport harness for the screen tests.
 *
 * Screen 05 of docs/design/grokbot-ui/README.md is the rule this file serves:
 * a phone opens the **same full web client** over the LAN — not a smaller app —
 * so every screen has to survive ~390 CSS px without a horizontal scrollbar.
 *
 * jsdom has no layout engine: `clientWidth` / `scrollWidth` /
 * `getBoundingClientRect().height` are all 0 and nothing reflows, so a test that
 * only read those numbers would pass whatever the CSS said. What jsdom *does*
 * keep is the CSS antd injects at runtime (antd 6 is CSS-in-JS) and resolve
 * `getComputedStyle` against it — measured, not assumed:
 *
 *   Button size="small" → height 24px · default → 32px · size="large" → 40px
 *   an inline `overflowWrap: 'anywhere'` → computed `anywhere`
 *
 * Those are the facts that decide whether a narrow screen overflows, so the
 * helpers below assert them, together with the width itself, installed the way
 * the app reads it: `window.innerWidth` plus a `matchMedia` that answers
 * `(max-width: …)` honestly. `useMediaQuery()` (`@/design/use-system-appearance`)
 * is the shell's only breakpoint mechanism, so a screen that stacks or grows its
 * tap targets at 390 has to be driven through it — and this stub is what makes
 * that branch reachable in a unit test.
 *
 * `expectNoHorizontalOverflow` is kept as the invariant the screen promises
 * (and the assertion the real Chromium pass in the change report makes with
 * actual layout); under jsdom both operands are 0, which is why it is never the
 * only assertion in a narrow-layout test.
 */
import { expect } from 'vitest';

/** The narrow viewport the pass is written against (iPhone-class, 390×844). */
export const PHONE_WIDTH = 390;
/** A desktop width, for the "the fix is narrow-only" half of a check. */
export const DESKTOP_WIDTH = 1280;

type ChangeListener = (event: { matches: boolean; media: string }) => void;

interface MediaQueryListLike {
  readonly matches: boolean;
  readonly media: string;
  onchange: ChangeListener | null;
  addListener: (listener: ChangeListener) => void;
  removeListener: (listener: ChangeListener) => void;
  addEventListener: (type: string, listener: ChangeListener) => void;
  removeEventListener: (type: string, listener: ChangeListener) => void;
  dispatchEvent: () => boolean;
}

let installed = false;
let width = PHONE_WIDTH;
let height = 844;
let originalMatchMedia: typeof window.matchMedia | null = null;
let originalInnerWidth = 0;
let originalInnerHeight = 0;
/** Live listeners per query, so a width change after mount still notifies. */
const listeners = new Map<string, Set<ChangeListener>>();

/**
 * Answers one media query against the installed width.
 *
 * Only the width features the client actually uses are evaluated
 * (`max-width` / `min-width`, in px); everything else — `prefers-color-scheme`,
 * `hover`, `pointer` — is answered "no", which is the same fallback the shell
 * already documents for jsdom.
 */
export function evaluateQuery(query: string, atWidth = width): boolean {
  let matchedAny = false;
  const widthFeature = /\(\s*(max|min)-width\s*:\s*([\d.]+)px\s*\)/g;
  for (const match of query.matchAll(widthFeature)) {
    matchedAny = true;
    const limit = Number.parseFloat(match[2] ?? '0');
    const ok = match[1] === 'max' ? atWidth <= limit : atWidth >= limit;
    if (!ok) return false;
  }
  return matchedAny;
}

function makeMediaQueryList(query: string): MediaQueryListLike {
  const list: MediaQueryListLike = {
    // A getter, not a snapshot: `useMediaQuery` re-reads `media.matches` after a
    // resize without re-creating the object.
    get matches() {
      return evaluateQuery(query);
    },
    media: query,
    onchange: null,
    addListener: (listener) => {
      register(query, listener);
    },
    removeListener: (listener) => {
      listeners.get(query)?.delete(listener);
    },
    addEventListener: (_type, listener) => {
      // antd and `useMediaQuery` both subscribe here; a resize re-reads.
      register(query, listener);
    },
    removeEventListener: (_type, listener) => {
      listeners.get(query)?.delete(listener);
    },
    dispatchEvent: () => false,
  };
  return list;
}

function register(query: string, listener: ChangeListener): void {
  if (!listeners.has(query)) listeners.set(query, new Set());
  listeners.get(query)?.add(listener);
}

/**
 * Put the page at `nextWidth` (default 390) and install a width-aware
 * `matchMedia`. Call **before** `render()`: the screens read the breakpoint on
 * mount, exactly as they do in a browser.
 *
 * Returns the uninstaller, and is registered for cleanup by
 * `installViewportCleanup()` when a suite forgets.
 */
export function installViewport(nextWidth = PHONE_WIDTH, nextHeight = 844): () => void {
  if (!installed) {
    originalMatchMedia = window.matchMedia;
    originalInnerWidth = window.innerWidth;
    originalInnerHeight = window.innerHeight;
    installed = true;
  }
  width = nextWidth;
  height = nextHeight;
  setWindowSize();
  window.matchMedia = ((query: string) =>
    makeMediaQueryList(query)) as unknown as typeof window.matchMedia;
  window.dispatchEvent(new Event('resize'));
  return uninstallViewport;
}

/** Change the width again after `installViewport` (fires `change` + `resize`). */
export function setViewportWidth(nextWidth: number, nextHeight = height): void {
  width = nextWidth;
  height = nextHeight;
  setWindowSize();
  for (const [query, set] of listeners) {
    const event = { matches: evaluateQuery(query), media: query };
    for (const listener of set) listener(event);
  }
  window.dispatchEvent(new Event('resize'));
}

function setWindowSize(): void {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    writable: true,
    value: width,
  });
  Object.defineProperty(window, 'innerHeight', {
    configurable: true,
    writable: true,
    value: height,
  });
}

/** Restore jsdom's own `matchMedia` stub and the default 1024px width. */
export function uninstallViewport(): void {
  if (!installed) return;
  installed = false;
  listeners.clear();
  if (originalMatchMedia) window.matchMedia = originalMatchMedia;
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    writable: true,
    value: originalInnerWidth,
  });
  Object.defineProperty(window, 'innerHeight', {
    configurable: true,
    writable: true,
    value: originalInnerHeight,
  });
  window.dispatchEvent(new Event('resize'));
}

/** The width a test installed, for assertion messages. */
export function viewportWidth(): number {
  return width;
}

/** `getComputedStyle`, typed as the CSS the screen actually renders with. */
export function computedStyle(element: Element): CSSStyleDeclaration {
  return window.getComputedStyle(element);
}

/**
 * The rendered height in px, from computed style.
 *
 * `NaN`-safe: a value jsdom cannot resolve (`auto`) reads as 0, which is what a
 * tap-target check has to treat as "no height to hit".
 */
export function renderedHeight(element: Element): number {
  const value = computedStyle(element).height;
  if (!value.endsWith('px')) return 0;
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

/**
 * A primary action must stay hittable at 390px: at least 36px tall (antd's
 * `middle` is 32px, so this forces an explicit decision — `large` is 40px).
 */
export function expectTapTarget(element: Element, minimum = 36): void {
  expect(
    renderedHeight(element),
    `tap target "${element.textContent?.trim() ?? ''}" is ${renderedHeight(element)}px tall at ${width}px`,
  ).toBeGreaterThanOrEqual(minimum);
}

/**
 * The screen itself must not scroll sideways.
 *
 * Under jsdom both operands are 0 (no layout), so pair this with a wrap /
 * ellipsis assertion; in a real engine this is the fact that failed before the
 * fix (`scrollWidth` 1442–2255 against a 390px `clientWidth`).
 */
export function expectNoHorizontalOverflow(element: Element, what = 'the screen'): void {
  const { scrollWidth, clientWidth } = element as HTMLElement;
  expect(
    scrollWidth,
    `${what} scrolls horizontally at ${width}px: scrollWidth ${scrollWidth} > clientWidth ${clientWidth}`,
  ).toBeLessThanOrEqual(clientWidth);
}

/**
 * Long text — a task subject, a URL, a credential id — has to wrap or ellipsise
 * instead of widening its container. Either mechanism is accepted because both
 * are honest: wrapping keeps the whole value readable, ellipsis gives it a
 * maximum (`Typography.Text ellipsis`).
 */
export function expectWrapsOrEllipsis(element: Element, what = 'the text'): void {
  const style = computedStyle(element);
  const wraps =
    style.overflowWrap === 'anywhere' ||
    style.overflowWrap === 'break-word' ||
    style.wordBreak === 'break-word' ||
    style.wordBreak === 'break-all';
  const ellipsises =
    style.textOverflow === 'ellipsis' &&
    style.whiteSpace === 'nowrap' &&
    (style.overflow === 'hidden' || style.overflowX === 'hidden');
  expect(
    wraps || ellipsises,
    `${what} would widen the container at ${width}px: overflow-wrap "${style.overflowWrap}", ` +
      `white-space "${style.whiteSpace}", text-overflow "${style.textOverflow}"`,
  ).toBe(true);
}

/** A flex child that must be allowed to shrink below its content's width. */
export function expectCanShrink(element: Element, what = 'the flex child'): void {
  expect(
    computedStyle(element).minWidth,
    `${what} keeps min-width "auto" at ${width}px, so it refuses to shrink below its longest word`,
  ).toBe('0px');
}