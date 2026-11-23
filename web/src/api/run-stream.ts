import { XRequest, type AbstractXRequestClass } from '@ant-design/x-sdk';
import type { SSEOutput } from '@ant-design/x-sdk';
import type { ZeoMessage } from '@/chat/types';
import type { RunRequest } from './protocol';

/**
 * The single run transport: the stock `XRequest` POSTing to
 * `POST /api/chat/run` (docs/TRANSPORT.md §4.6, docs/plans/web-runtime-adapter.md
 * §3 decision B).
 *
 * `XRequest` is SSE-specific and already owns the fetch, the ReadableStream
 * parse, and `AbortController` — so the abstraction has one implementation and
 * no hand-written stream reader lives in this repository.
 */
export const RUN_ENDPOINT = '/api/chat/run';

export function createRunRequest(
  baseURL: string = RUN_ENDPOINT,
  /**
   * Extra headers. In the browser there are none: the gateway authenticates the
   * run with the `zk` cookie (§3.1). Non-browser callers (the integration test,
   * curl) use `Authorization: Bearer <token>` instead.
   */
  extraHeaders: Record<string, string> = {},
): RunRequestClient {
  return XRequest<RunRequest, SSEOutput, ZeoMessage>(baseURL, {
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      ...extraHeaders,
    },
    // No request timeout: a turn can legitimately take minutes. The stream
    // timeout catches a stalled gateway instead (50–100ms coalescing means a
    // healthy run always emits something well inside this window).
    streamTimeout: 60_000,
    // AbstractChatProvider refuses a non-manual request: useXChat calls run()
    // itself for every turn, so the request must never fire on construction.
    manual: true,
  });
}

export type RunRequestClient = AbstractXRequestClass<RunRequest, SSEOutput, ZeoMessage>;