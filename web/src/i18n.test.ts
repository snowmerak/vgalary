import { afterEach, describe, expect, it, vi } from 'vitest';
import { detectLocale, errorSummary, getLocale, locales, number, setLocale, translate } from './i18n';
import { messages, type MessageKey } from './messages';
import { bytes, date, filename } from './utils';
import type { Asset } from './types';

afterEach(() => {
  setLocale('en');
  vi.unstubAllGlobals();
});

describe('locale selection', () => {
  it.each([
    [null, ['ko-KR', 'en'], 'ko'],
    ['ja', ['ko-KR'], 'ja'],
    ['unknown', ['zh-TW'], 'zh-Hant'],
    [null, ['zh-HK'], 'zh-Hant'],
    [null, ['zh-MO'], 'zh-Hant'],
    [null, ['zh-CN'], 'zh-Hans'],
    [null, ['zh-SG'], 'zh-Hans'],
    [null, ['zh'], 'zh-Hans'],
    [null, ['zh-Hans-TW'], 'zh-Hans'],
    [null, ['zh-Hant-CN'], 'zh-Hant'],
    [null, ['ja-JP'], 'ja'],
    [null, ['en-GB'], 'en'],
    [null, ['de-DE', 'ja-JP'], 'ja'],
    [null, ['fr-FR'], 'en'],
    [null, [], 'en'],
  ] as const)('detects %s / %j as %s', (saved, languages, expected) => {
    expect(detectLocale(saved, languages)).toBe(expected);
  });

  it('persists an explicit selection without accessing server settings', () => {
    const setItem = vi.fn();
    vi.stubGlobal('window', { localStorage: { setItem } });
    setLocale('zh-Hant');
    expect(setItem).toHaveBeenCalledWith('vgalary.language', 'zh-Hant');
    expect(getLocale()).toBe('zh-Hant');
  });

  it('still switches languages when browser storage is blocked', () => {
    vi.stubGlobal('window', {
      localStorage: {
        setItem: () => {
          throw new Error('blocked');
        },
      },
    });
    expect(() => setLocale('ja')).not.toThrow();
    expect(getLocale()).toBe('ja');
  });
});

describe('complete translations and interpolation', () => {
  for (const [key, translations] of Object.entries(messages)) {
    it(`translates ${key} in all five languages with matching placeholders`, () => {
      const placeholders = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
      expect(translations).toHaveLength(4);
      for (const language of locales) {
        const translated = translate(language, key as MessageKey);
        expect(translated.trim()).not.toBe('');
        expect(placeholders(translated)).toEqual(placeholders(key));
      }
    });
  }

  it('interpolates entire messages and leaves user filenames untouched', () => {
    const name = '사진/猫 {count}.jpg';
    for (const language of locales) {
      expect(translate(language, '{name} 자세히 보기', { name })).toContain(name);
      expect(translate(language, '미디어 {count}개', { count: 12345 })).toContain(
        new Intl.NumberFormat(language).format(12345),
      );
    }
    const asset = { location: { relative_path: name } } as Asset;
    setLocale('ja');
    expect(filename(asset)).toBe('猫 {count}.jpg');
  });

  it('formats numbers, sizes, and dates with the active locale', () => {
    const instant = '2026-10-31T12:30:00Z';
    for (const language of locales) {
      setLocale(language);
      expect(number(12345)).toBe(new Intl.NumberFormat(language).format(12345));
      expect(bytes(1536)).toBe(
        `${new Intl.NumberFormat(language, { maximumFractionDigits: 1 }).format(1.5)} KB`,
      );
      expect(date(instant)).toBe(
        new Intl.DateTimeFormat(language, {
          month: 'short',
          day: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
        }).format(new Date(instant)),
      );
    }
  });

  it('localizes API error summaries while preserving technical details separately', () => {
    setLocale('ko');
    expect(errorSummary('[HTTP 401] unauthorized')).toBe('로그인이 필요합니다.');
    setLocale('ja');
    expect(errorSummary('[HTTP 409] original file changed')).toBe('現在の状態とリクエストが競合しています。');
    setLocale('en');
    expect(errorSummary('Failed to fetch')).toBe('Unable to reach the server.');
    expect(errorSummary('[HTTP 401] password does not match')).toBe('Incorrect password.');
  });
});
