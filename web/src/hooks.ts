import { t } from './i18n';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api';

export function useResource<T>(path: string, interval = 0, refreshKey = '') {
  const [data, setData] = useState<T>();
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    const controller = new AbortController();
    let running = false;
    const load = async () => {
      if (running) return;
      running = true;
      try {
        const result = await api<T>(path, { signal: controller.signal });
        if (!controller.signal.aborted) {
          setData(result);
          setError('');
        }
      } catch (cause) {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : t('데이터를 불러오지 못했습니다.'));
      } finally {
        running = false;
      }
    };
    void load();
    const timer = interval ? window.setInterval(() => void load(), interval) : undefined;
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [path, interval, revision, refreshKey]);
  return { data, error, refresh };
}

export function useActions(notify: (message: string, error?: boolean) => void) {
  const [pending, setPending] = useState<Set<string>>(new Set());
  const active = useRef(new Set<string>());
  const run = async (key: string, action: () => Promise<void>) => {
    if (active.current.has(key)) return;
    active.current.add(key);
    setPending(new Set(active.current));
    try {
      await action();
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : t('요청을 처리하지 못했습니다.'), true);
    } finally {
      active.current.delete(key);
      setPending(new Set(active.current));
    }
  };
  return { pending, run };
}
