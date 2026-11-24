/**
 * Attachment blobs (TRANSPORT §6.4): the bytes go to `POST /api/blobs`, and the run
 * request carries only the returned id. The message keeps the name/mime/size, so a
 * reloaded conversation still shows what was attached.
 */

export interface StagedAttachment {
  /** Blob id from the upload. */
  id: string;
  name: string;
  mime: string;
  bytes: number;
  /** Local preview URL for an image; revoked when the chip is removed. */
  previewUrl?: string;
}

/** The gateway refuses anything larger; failing here gives a clear message first. */
export const ATTACHMENT_MAX_BYTES = 10 << 20;

export async function uploadBlob(file: File): Promise<StagedAttachment> {
  if (file.size > ATTACHMENT_MAX_BYTES) {
    throw new Error('attachment_too_large');
  }
  const res = await fetch(`/api/blobs?name=${encodeURIComponent(file.name)}`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream', Accept: 'application/json' },
    body: file,
  });
  if (!res.ok) {
    throw new Error(`blob_upload_failed:${res.status}`);
  }
  const body = (await res.json()) as { id: string; mime: string; bytes: number; name?: string };
  return {
    id: body.id,
    name: body.name || file.name,
    mime: body.mime,
    bytes: body.bytes,
    ...(file.type.startsWith('image/') ? { previewUrl: URL.createObjectURL(file) } : {}),
  };
}

/** One attachment's bytes URL (thumbnails); the gateway requires the process cookie. */
export function blobURL(id: string): string {
  return `/api/blobs/${encodeURIComponent(id)}`;
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Staged metadata by blob id.
 *
 * The optimistic bubble is built from the request params, which carry the upload
 * ids; the names live here until the durable snapshot takes over. Without it the
 * user's own message shows the attachment only after a reload.
 */
const staged = new Map<string, { id: string; name: string; mime: string; bytes: number }>();

export function rememberStaged(items: Array<{ id: string; name: string; mime: string; bytes: number }>): void {
  for (const item of items) staged.set(item.id, item);
}

export function lookupStaged(id: string): { id: string; name: string; mime: string; bytes: number } | undefined {
  return staged.get(id);
}
