import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { en, type Dictionary } from "./en";
import { zh } from "./zh";

export type { Dictionary } from "./en";

export type Locale = "en" | "zh";

type I18n = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
};

const dictionaries: Record<Locale, Dictionary> = { en, zh };
const storageKey = "mnemon-language";
const I18nContext = createContext<I18n | null>(null);

function parseLocale(value: string | null): Locale | null {
  return value === "en" || value === "zh" ? value : null;
}

function readStoredLocale(): Locale | null {
  try {
    return parseLocale(window.localStorage.getItem(storageKey));
  } catch {
    return null;
  }
}

function detectLocale(): Locale {
  const preferred = readStoredLocale();
  if (preferred) return preferred;
  try {
    const languages = window.navigator.languages?.length
      ? window.navigator.languages
      : [window.navigator.language];
    return languages.some((tag) => tag?.toLowerCase().startsWith("zh"))
      ? "zh"
      : "en";
  } catch {
    return "en";
  }
}

function interpolate(
  template: string,
  params?: Record<string, string | number>,
): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match,
  );
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(detectLocale);

  useEffect(() => {
    document.documentElement.lang = locale === "zh" ? "zh-CN" : "en";
  }, [locale]);

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key !== storageKey && event.key !== null) return;
      try {
        if (event.storageArea && event.storageArea !== window.localStorage)
          return;
      } catch {
        // Storage access may be denied while event delivery remains available.
      }
      setLocaleState(
        event.key === null
          ? detectLocale()
          : (parseLocale(event.newValue) ?? detectLocale()),
      );
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    try {
      window.localStorage.setItem(storageKey, next);
    } catch {
      // The choice still applies to this tab when persistence is unavailable.
    }
  }, []);

  return (
    <I18nContext.Provider value={{ locale, setLocale }}>
      {children}
    </I18nContext.Provider>
  );
}

export function useI18n(): I18n {
  const value = useContext(I18nContext);
  if (!value) throw new Error("useI18n must be used within I18nProvider");
  return value;
}

export function useT() {
  const { locale } = useI18n();
  const dictionary = dictionaries[locale];
  return useCallback(
    (key: keyof Dictionary, params?: Record<string, string | number>) =>
      interpolate(dictionary[key], params),
    [dictionary],
  );
}
