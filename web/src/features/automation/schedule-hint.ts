/**
 * The schedule grammar the gateway actually accepts (`contracts.ParseAutomationSchedule`).
 *
 * The form's help line used to say "五段 cron（如 0 8 * * *）", but a bare five-field
 * cron is *not* accepted — it needs the `cron` prefix — and `at`, `after`,
 * `weekly`, `@hourly` were not mentioned at all. A user following the help got a
 * failure from the gateway with no local warning (round-2 automation audit).
 */
const DURATION = /^\d+(s|m|h|d)$/;
const HHMM = /^\d{1,2}:\d{2}$/;
const WEEKDAYS = new Set(['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']);

function validClock(value: string): boolean {
  if (!HHMM.test(value)) return false;
  const [hours, minutes] = value.split(':').map(Number);
  return hours >= 0 && hours < 24 && minutes >= 0 && minutes < 60;
}

/** Null when the expression is one the gateway will accept, else a catalogue key. */
export function scheduleProblem(
  spec: string,
): 'format' | 'clock' | 'needsCronPrefix' | null {
  const text = spec.trim().toLowerCase();
  if (text === '') return null;
  if (text === '@hourly' || text === '@daily' || text === '@midnight') return null;

  const fields = text.split(/\s+/);
  const [head, ...rest] = fields;

  if (head === 'every' || head === 'after') {
    return rest.length === 1 && DURATION.test(rest[0]) ? null : 'format';
  }
  if (head === 'daily') {
    if (rest.length === 0) return null;
    return rest.length === 1 && validClock(rest[0]) ? null : 'clock';
  }
  if (head === 'weekly') {
    if (rest.length === 0) return null;
    if (!WEEKDAYS.has(rest[0])) return 'format';
    return rest.length === 1 || (rest.length === 2 && validClock(rest[1])) ? null : 'clock';
  }
  if (head === 'at') {
    return rest.length === 0 ? null : rest.length === 1 && validClock(rest[0]) ? null : 'clock';
  }
  if (head === 'cron') {
    return rest.length === 5 ? null : 'format';
  }
  // A bare five-field expression is the trap the old help text set.
  if (fields.length === 5 && fields.every((field) => /^[\d*/,-]+$/.test(field))) {
    return 'needsCronPrefix';
  }
  return 'format';
}
