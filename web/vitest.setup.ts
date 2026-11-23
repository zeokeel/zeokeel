import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';

// antd (and @ant-design/x) query media queries on mount; jsdom ships no
// implementation, so every component test needs this stub before rendering.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

// jsdom has no IntersectionObserver; @ant-design/x's BubbleList uses it for
// scroll-compatibility detection.
if (!('IntersectionObserver' in globalThis)) {
  class NoopIntersectionObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  }
  Object.defineProperty(globalThis, 'IntersectionObserver', {
    value: NoopIntersectionObserver,
  });
}

// jsdom has no ResizeObserver; BubbleList and antd's motion hooks observe sizes.
if (!('ResizeObserver' in globalThis)) {
  class NoopResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  Object.defineProperty(globalThis, 'ResizeObserver', { value: NoopResizeObserver });
}
// Testing Library's automatic cleanup relies on a global `afterEach`; this
// config imports vitest's explicitly, so register it here.
afterEach(() => {
  cleanup();
});
