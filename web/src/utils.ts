import { t, getLocale, number } from './i18n';
import type { Asset, Result, Source } from './types';

export function filename(asset: Asset): string {
  const entries = asset.location.archive_entries;
  return (
    (entries?.length ? entries[entries.length - 1].name : asset.location.relative_path)
      .replaceAll('\\', '/')
      .split('/')
      .pop() || t('미디어')
  );
}
export function displayPath(asset: Asset, sources: Source[]): string {
  const source = sources.find((item) => item.id === asset.source_id);
  const root = source?.root_path.replace(/[\\/]+$/, '');
  const path = root ? `${root}/${asset.location.relative_path}` : asset.location.relative_path;
  return path + (asset.location.archive_entries || []).map((entry) => `!/${entry.name}`).join('');
}
export function timestamp(milliseconds: number): string {
  const ms = Math.max(0, Math.round(milliseconds));
  return `${String(Math.floor(ms / 3600000)).padStart(2, '0')}:${String(Math.floor(ms / 60000) % 60).padStart(2, '0')}:${String(Math.floor(ms / 1000) % 60).padStart(2, '0')}.${String(ms % 1000).padStart(3, '0')}`;
}
export function bytes(value: number): string {
  if (!value) return '0 B';
  const i = Math.min(3, Math.floor(Math.log(value) / Math.log(1024)));
  return `${number(value / 1024 ** i, { maximumFractionDigits: 1 })} ${['B', 'KB', 'MB', 'GB'][i]}`;
}
export function date(value?: string): string {
  return value
    ? new Intl.DateTimeFormat(getLocale(), {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      }).format(new Date(value))
    : '—';
}
export function libraryResult(asset: Asset, sources: Source[]): Result {
  const point = asset.points[0];
  return {
    asset,
    point_id: point?.id || '',
    timestamp_ms: point?.timestamp_ms,
    path: displayPath(asset, sources),
    thumbnail_url: point ? `/api/assets/${asset.id}/thumbnail?point_id=${point.id}` : undefined,
  };
}
export function sourceName(source?: Source): string {
  return (
    source?.root_path
      .replace(/[\\/]+$/, '')
      .split(/[\\/]/)
      .pop() || t('디렉토리')
  );
}
