import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { SETTING_KEYS } from '@/design/settings-keys';
import { subscribeSettings } from '@/api/gateway';
import { en } from './en';
import { zh, type Catalog, type MessageKey } from './zh';

/**
 * Minimal i18n: two languages, `{name}` interpolation, no library.
 *
 * The language comes from the `settings` domain row (`ui.language`, generated
 * key `SETTING_KEYS.uiLanguage`) and applies on the next delta — no reload. With
 * no row, the browser's own preference decides (`zh*` → Chinese, everything else
 * → English), which is what a user who never opened settings would expect.
 *
 * Protocol values (`stopReason`, `errorKind`, tool names) are **not** copy and
 * have no keys here: they are branch conditions (README 硬约束「协议值不进 UI」).
 */
export type Locale = 'zh' | 'en';

export const CATALOGS: Record<Locale, Catalog> = { zh, en };

/** `zh-CN`, `zh-Hant`, `zh` → zh; anything else → en. */
export function localeFromTag(tag: string | undefined | null): Locale {
  if (!tag) return 'zh';
  return tag.trim().toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

/** The stored row wins; the browser preference is the fallback. */
export function resolveLocale(rowValue: string | undefined, browserTag: string | undefined): Locale {
  if (rowValue === 'zh' || rowValue === 'en') return rowValue;
  return localeFromTag(browserTag);
}

export function translate(
  locale: Locale,
  key: MessageKey,
  params?: Record<string, string | number>,
): string {
  const template = CATALOGS[locale][key] ?? zh[key] ?? key;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  );
}

interface I18nApi {
  locale: Locale;
  t: (key: MessageKey, params?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nApi | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocale] = useState<Locale>(() =>
    resolveLocale(
      undefined,
      typeof navigator === 'undefined' ? undefined : navigator.language,
    ),
  );

  useEffect(() => {
    // The settings domain is the source of truth; the first snapshot and every
    // later delta (the user picks a language) both land here.
    return subscribeSettings((rows) => {
      setLocale(
        resolveLocale(
          rows[SETTING_KEYS.uiLanguage],
          typeof navigator === 'undefined' ? undefined : navigator.language,
        ),
      );
    });
  }, []);

  // The document's own language follows the UI language: an English interface
  // announced as `lang="zh-CN"` makes a screen reader read it with the wrong
  // pronunciation rules (round-3 mobile audit O4).
  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = locale === 'en' ? 'en' : 'zh-CN';
    }
  }, [locale]);

  const value = useMemo<I18nApi>(
    () => ({ locale, t: (key, params) => translate(locale, key, params) }),
    [locale],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/**
 * Copy must never crash a surface: a component rendered outside the provider
 * (a unit test, a story, a future isolated mount) falls back to Chinese instead
 * of throwing. The app itself always installs the provider, so the fallback is
 * a safety net, not a second source of truth.
 */
const FALLBACK: I18nApi = {
  locale: 'zh',
  t: (key, params) => translate('zh', key, params),
};

export function useI18n(): I18nApi {
  return useContext(I18nContext) ?? FALLBACK;
}

/** Shorthand: `const t = useT()` then `t('shell.settings')`. */
export function useT(): I18nApi['t'] {
  return useI18n().t;
}

export type { MessageKey };