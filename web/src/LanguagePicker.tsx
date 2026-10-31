import { Languages } from 'lucide-react';
import { languageNames, locales, setLocale, t, useLocale, type Locale } from './i18n';

export function LanguagePicker() {
  const locale = useLocale();
  return (
    <label className="language-picker">
      <Languages size={17} aria-hidden="true" />
      <span className="sr-only">{t('언어')}</span>
      <select value={locale} onChange={(event) => setLocale(event.target.value as Locale)}>
        {locales.map((language) => (
          <option key={language} value={language} lang={language}>
            {languageNames[language]}
          </option>
        ))}
      </select>
    </label>
  );
}
