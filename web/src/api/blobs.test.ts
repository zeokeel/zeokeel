import { describe, expect, it } from 'vitest';
import { ATTACHMENT_MAX_BYTES, formatBytes, uploadBlob } from './blobs';

/**
 * Attachment blobs (TRANSPORT §6.4): the client uploads the bytes and the run only
 * names the returned id. These pin the contract the gateway relies on.
 */
describe('blob upload', () => {
  it('posts the file with its own content type and returns the staged chip', async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    const original = globalThis.fetch;
    globalThis.fetch = (async (url: string, init: RequestInit) => {
      calls.push({ url: String(url), init });
      return new Response(JSON.stringify({ id: 'blob-1', mime: 'image/png', bytes: 3, name: 'a.png' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;
    try {
      const file = new File([new Uint8Array([1, 2, 3])], 'a.png', { type: 'image/png' });
      const staged = await uploadBlob(file);
      expect(staged).toMatchObject({ id: 'blob-1', name: 'a.png', mime: 'image/png', bytes: 3 });
      expect(calls[0].url).toContain('/api/blobs?name=a.png');
      expect((calls[0].init.headers as Record<string, string>)['Content-Type']).toBe('image/png');
    } finally {
      globalThis.fetch = original;
    }
  });

  it('refuses an oversized file before uploading it', async () => {
    let called = false;
    const original = globalThis.fetch;
    globalThis.fetch = (async () => {
      called = true;
      return new Response('{}', { status: 200 });
    }) as typeof fetch;
    try {
      const big = new File([new Uint8Array(ATTACHMENT_MAX_BYTES + 1)], 'big.bin', { type: 'application/octet-stream' });
      await expect(uploadBlob(big)).rejects.toThrow('attachment_too_large');
      expect(called).toBe(false);
    } finally {
      globalThis.fetch = original;
    }
  });

  it('formats sizes the way the chip shows them', () => {
    expect(formatBytes(70)).toBe('70 B');
    expect(formatBytes(2048)).toBe('2.0 KB');
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB');
  });
});
