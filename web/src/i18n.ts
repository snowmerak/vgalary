import { useSyncExternalStore } from 'react';
import { messages, type MessageKey } from './messages';

export const locales = ['en', 'ko', 'zh-Hans', 'zh-Hant', 'ja'] as const;
export type Locale = (typeof locales)[number];
export const languageNames: Record<Locale, string> = {
  en: 'English',
  ko: '한국어',
  'zh-Hans': '简体中文',
  'zh-Hant': '繁體中文',
  ja: '日本語',
};
const storageKey = 'vgalary.language';

export function detectLocale(saved: string | null, languages: readonly string[]): Locale {
  if (locales.includes(saved as Locale)) return saved as Locale;
  for (const language of languages) {
    const tag = language.toLowerCase().replaceAll('_', '-');
    if (tag === 'zh' || tag.startsWith('zh-')) {
      if (tag.includes('-hant')) return 'zh-Hant';
      if (tag.includes('-hans')) return 'zh-Hans';
      return /-(tw|hk|mo)(-|$)/.test(tag) ? 'zh-Hant' : 'zh-Hans';
    }
    for (const supported of ['en', 'ko', 'ja'] as const) {
      if (tag === supported || tag.startsWith(`${supported}-`)) return supported;
    }
  }
  return 'en';
}

function initialLocale(): Locale {
  if (typeof window === 'undefined') return 'en';
  let saved: string | null = null;
  try {
    saved = window.localStorage.getItem(storageKey);
  } catch {
    /* Storage can be blocked. */
  }
  return detectLocale(saved, window.navigator.languages);
}

let locale = initialLocale();
const listeners = new Set<() => void>();
export function getLocale(): Locale {
  return locale;
}
export function setLocale(value: Locale) {
  if (!locales.includes(value)) return;
  try {
    window.localStorage.setItem(storageKey, value);
  } catch {
    /* Keep the in-memory selection. */
  }
  if (locale === value) return;
  locale = value;
  for (const listener of listeners) listener();
}
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
export function useLocale() {
  return useSyncExternalStore(subscribe, getLocale, () => 'en' as Locale);
}

export function translate(
  language: Locale,
  key: MessageKey,
  values: Record<string, string | number> = {},
): string {
  const index = { en: 0, 'zh-Hans': 1, 'zh-Hant': 2, ja: 3 } as const;
  const message = language === 'ko' ? key : messages[key][index[language]];
  return message.replace(/\{(\w+)\}/g, (placeholder, name: string) => {
    const value = values[name];
    return typeof value === 'number' ? new Intl.NumberFormat(language).format(value) : (value ?? placeholder);
  });
}
export function t(key: MessageKey, values?: Record<string, string | number>): string {
  return translate(locale, key, values);
}
export function number(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale, options).format(value);
}

export function errorSummary(message: string): string {
  const status = Number(/\[HTTP (\d+)\]/.exec(message)?.[1]);
  if (status === 401 && /password does not match|incorrect password/i.test(message)) {
    return t('비밀번호가 올바르지 않습니다.');
  }
  if (/failed to fetch|networkerror|load failed/i.test(message)) return t('서버에 연결할 수 없습니다.');
  const key: MessageKey =
    status === 401
      ? '로그인이 필요합니다.'
      : status === 403
        ? '요청이 거부되었습니다.'
        : status === 404
          ? '항목을 찾을 수 없습니다.'
          : status === 409
            ? '요청이 충돌했습니다.'
            : status === 400
              ? '잘못된 요청입니다.'
              : status === 502 || status === 503 || status === 504
                ? '외부 서비스 연결에 실패했습니다.'
                : '요청을 처리하지 못했습니다.';
  return t(key);
}
