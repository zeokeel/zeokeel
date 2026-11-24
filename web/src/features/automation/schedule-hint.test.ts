import { describe, expect, it } from 'vitest';
import { scheduleProblem } from './schedule-hint';

/**
 * The grammar mirror (`contracts.ParseAutomationSchedule`). The help text used to
 * promise that a bare five-field cron worked; it does not.
 */
describe('scheduleProblem', () => {
  it.each(['@hourly', '@daily', '@midnight', 'daily', 'daily 09:00', 'daily 23:59'])(
    'accepts %s',
    (spec) => expect(scheduleProblem(spec)).toBeNull(),
  );

  it.each(['every 24h', 'every 30m', 'after 2h', 'weekly', 'weekly mon', 'weekly mon 09:00', 'at 09:00', 'cron 0 8 * * *'])(
    'accepts %s',
    (spec) => expect(scheduleProblem(spec)).toBeNull(),
  );

  it('refuses the trap the old help text set', () => {
    expect(scheduleProblem('0 8 * * *')).toBe('needsCronPrefix');
  });

  it('refuses bad clocks and unknown words', () => {
    expect(scheduleProblem('daily 9am')).toBe('clock');
    expect(scheduleProblem('daily 25:00')).toBe('clock');
    expect(scheduleProblem('weekly someday')).toBe('format');
    expect(scheduleProblem('tomorrow')).toBe('format');
    expect(scheduleProblem('every day')).toBe('format');
    expect(scheduleProblem('cron 0 8 * *')).toBe('format');
  });

  it('is silent on an empty value (the required rule owns that)', () => {
    expect(scheduleProblem('   ')).toBeNull();
  });
});
