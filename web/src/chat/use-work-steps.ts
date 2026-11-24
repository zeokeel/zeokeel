import { useEffect, useState } from 'react';
import { subscribeSettings, type SettingsRows } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';

/**
 * How much of a turn's work to show (`ui.workSteps.mode`) and when the settled
 * steps fold away (`ui.workSteps.collapse`).
 *
 * Both rows existed in 设置 → 外观 and drove nothing: every turn rendered the
 * same chain and always folded when it settled (round-2 S13). They are read here
 * once per screen and passed down, so the transcript stays a pure render of its
 * props.
 */
/** The values the settings row actually stores (`ia.ts` radio options). */
export type WorkStepsMode = 'compact' | 'standard' | 'detailed' | 'verbose';
export type WorkStepsCollapse = 'completion' | 'next-input';

export interface WorkStepsConfig {
  mode: WorkStepsMode;
  collapse: WorkStepsCollapse;
}

const MODES: WorkStepsMode[] = ['compact', 'standard', 'detailed', 'verbose'];

export function readWorkSteps(rows: SettingsRows): WorkStepsConfig {
  const rawMode = String(rows[SETTING_KEYS.workStepsMode] ?? 'standard');
  const mode = (MODES as string[]).includes(rawMode) ? (rawMode as WorkStepsMode) : 'standard';
  const collapse =
    String(rows[SETTING_KEYS.workStepsCollapse] ?? 'completion') === 'next-input'
      ? 'next-input'
      : 'completion';
  return { mode, collapse };
}

export function useWorkSteps(): WorkStepsConfig {
  const [rows, setRows] = useState<SettingsRows>({});
  useEffect(() => subscribeSettings(setRows), []);
  return readWorkSteps(rows);
}
