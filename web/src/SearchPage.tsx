import { t, number } from './i18n';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  Archive,
  ArrowUpRight,
  ChevronDown,
  Eye,
  Film,
  Image as ImageIcon,
  Search,
  Sparkles,
} from 'lucide-react';
import { api } from './api';
import { catalogPage, catalogPageSize } from './catalog';
import { Empty, ErrorBanner, Spinner } from './components';
import { filename, libraryResult, sourceName, timestamp } from './utils';
import type { Asset, MediaType, Result, Source } from './types';

function MediaCard({ result, onSelect }: { result: Result; onSelect: (result: Result) => void }) {
  const [failed, setFailed] = useState(false);
  const asset = result.asset;
  const video = asset.media_type === 'video';
  const archived = !!asset.location.archive_entries?.length;
  return (
    <button
      className="media-card"
      onClick={() => onSelect(result)}
      aria-label={t('{name} 자세히 보기', { name: filename(asset) })}
    >
      <div className="media-image">
        {result.thumbnail_url && !failed ? (
          <img
            src={result.thumbnail_url}
            alt={filename(asset)}
            loading="lazy"
            onError={() => setFailed(true)}
          />
        ) : (
          <div className="media-unavailable">
            {video ? <Film size={30} /> : <ImageIcon size={30} />}
            <span>{t('미리보기 없음')}</span>
          </div>
        )}
        {video && (
          <span className="video-time">
            <Film size={13} />
            {timestamp(result.timestamp_ms || 0).slice(0, 8)}
          </span>
        )}
        <span className="media-open">
          <Eye size={15} />
          {t('자세히 보기')}
        </span>
      </div>
      <div className="media-caption">
        <span className="media-name">
          {archived && <Archive size={14} />}
          {filename(asset)}
        </span>
        {result.score !== undefined && (
          <span className="media-score" title={t('Cosine 유사도')}>
            {number(result.score, { minimumFractionDigits: 3, maximumFractionDigits: 3 })}
          </span>
        )}
      </div>
      <p className="media-path" title={result.path}>
        {result.path}
      </p>
    </button>
  );
}

export function SearchPage({
  sources,
  refreshKey,
  profileID,
  onSelect,
  goSources,
}: {
  sources: Source[];
  refreshKey: string;
  profileID: string;
  onSelect: (result: Result) => void;
  goSources: () => void;
}) {
  const [query, setQuery] = useState('');
  const [request, setRequest] = useState({
    query: '',
    kind: '' as MediaType,
    source: '',
    offset: 0,
    revision: 0,
    refreshKey,
  });
  // A catalog revision always restarts at the first page, keeping the user's query and filters.
  const currentRequest = request.refreshKey === refreshKey ? request : { ...request, offset: 0, refreshKey };
  const { query: submitted, kind, source, offset } = currentRequest;
  const baseKey = JSON.stringify([submitted, kind, source, refreshKey, profileID]);
  const key = JSON.stringify([baseKey, offset, request.revision]);
  const [response, setResponse] = useState<{
    baseKey: string;
    key: string;
    items: Result[];
    hasMore: boolean;
    error: string;
  }>();
  const current = response?.baseKey === baseKey ? response : undefined;
  const busy = current?.key !== key;
  const error = busy ? '' : current?.error || '';
  const hasMore = current?.hasMore || false;
  const shown = (current?.items || []).map((result) =>
    submitted ? result : libraryResult(result.asset, sources),
  );
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const focus = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener('keydown', focus);
    return () => window.removeEventListener('keydown', focus);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        let items: Result[];
        if (submitted) {
          const result = await api<{ results: Result[] }>('/search', {
            method: 'POST',
            body: JSON.stringify({
              query: submitted,
              media_type: kind,
              source_id: source,
              limit: catalogPageSize + 1,
              offset,
            }),
            signal: controller.signal,
          });
          items = result.results;
        } else {
          const params = new URLSearchParams({
            limit: String(catalogPageSize + 1),
            offset: String(offset),
            media_type: kind,
            source_id: source,
            profile_id: profileID,
          });
          const assets = await api<Asset[]>(`/assets?${params}`, { signal: controller.signal });
          items = assets.map((asset) => libraryResult(asset, []));
        }
        if (!controller.signal.aborted) {
          const page = catalogPage(items);
          setResponse((previous) => ({
            baseKey,
            key,
            error: '',
            hasMore: page.hasMore,
            items:
              offset > 0 && previous?.baseKey === baseKey ? [...previous.items, ...page.items] : page.items,
          }));
        }
      } catch (cause) {
        if (!controller.signal.aborted)
          setResponse((previous) => ({
            baseKey,
            key,
            items: previous?.baseKey === baseKey ? previous.items : [],
            hasMore: previous?.baseKey === baseKey ? previous.hasMore : false,
            error: cause instanceof Error ? cause.message : t('미디어를 불러오지 못했습니다.'),
          }));
      }
    };
    void load();
    return () => controller.abort();
  }, [baseKey, key, submitted, kind, source, offset, profileID]);
  const reset = (changes: Partial<Pick<typeof request, 'query' | 'kind' | 'source'>>) =>
    setRequest((previous) => ({
      ...previous,
      ...changes,
      offset: 0,
      refreshKey,
      revision: previous.revision + 1,
    }));
  const retry = () =>
    setRequest((previous) => ({ ...previous, refreshKey, offset, revision: previous.revision + 1 }));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    reset({ query: query.trim() });
  };
  const changeKind = (value: MediaType) => reset({ kind: value });
  const changeSource = (value: string) => reset({ source: value });
  return (
    <section className="search-page">
      <div className="page-intro">
        <h1>{t('기억나는 장면을 찾아보세요.')}</h1>
        <p>{t('이미지와 영상, 압축 파일 속까지 자연어로 검색하세요.')}</p>
      </div>
      <form className="search-box" onSubmit={submit}>
        <Search size={24} strokeWidth={1.7} />
        <input
          ref={input}
          aria-label={t('자연어 검색')}
          placeholder={t('파란 고양이 장난감')}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <kbd>⌘ K</kbd>
        <button className="primary" type="submit" disabled={busy}>
          {busy ? <Spinner /> : t('검색')}
        </button>
      </form>
      <div className="filters">
        <div className="segmented" aria-label={t('미디어 종류')}>
          {(
            [
              ['', t('전체')],
              ['image', t('이미지')],
              ['video', t('영상')],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              aria-pressed={kind === value}
              className={kind === value ? 'selected' : ''}
              onClick={() => changeKind(value)}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="select-wrap">
          <select
            aria-label={t('검색 디렉토리')}
            value={source}
            onChange={(event) => changeSource(event.target.value)}
          >
            <option value="">{t('모든 디렉토리')}</option>
            {sources.map((item) => (
              <option key={item.id} value={item.id}>
                {sourceName(item)}
              </option>
            ))}
          </select>
          <ChevronDown size={16} />
        </div>
      </div>
      {error && <ErrorBanner message={error} retry={retry} />}
      <div className="results-heading">
        <h2>
          {submitted ? t('검색 결과') : t('라이브러리')}
          {submitted && <span>“{submitted}”</span>}
        </h2>
        <span>
          {submitted ? t('관련도순') : t('최근 색인순')}
          <ChevronDown size={14} />
        </span>
      </div>
      {busy && shown.length === 0 ? (
        <div className="loading">
          <Spinner />
          {submitted ? t('검색하고 있어요.') : t('라이브러리를 불러오고 있어요.')}
        </div>
      ) : shown.length > 0 ? (
        <>
          <div className={`gallery ${busy ? 'gallery-updating' : ''}`}>
            {shown.map((result) => (
              <MediaCard key={`${result.asset.id}:${result.point_id}`} result={result} onSelect={onSelect} />
            ))}
          </div>
          {hasMore && (
            <div className="load-more">
              <button
                className="secondary"
                disabled={busy}
                onClick={() =>
                  setRequest((previous) => ({
                    ...previous,
                    refreshKey,
                    offset: shown.length,
                    revision: previous.revision + 1,
                  }))
                }
              >
                {busy ? <Spinner /> : t('더 보기')}
              </button>
            </div>
          )}
        </>
      ) : error ? null : (
        <Empty
          icon={submitted ? <Search size={30} /> : <Sparkles size={30} />}
          title={
            submitted || kind || source
              ? t('일치하는 미디어가 없어요.')
              : t('나만의 라이브러리를 연결해 보세요.')
          }
          action={
            !submitted &&
            !kind &&
            !source && (
              <button className="primary" onClick={goSources}>
                {t('디렉토리 추가')}
                <ArrowUpRight size={16} />
              </button>
            )
          }
        >
          {submitted || kind || source
            ? t('다른 표현을 사용하거나 미디어 종류와 디렉토리 필터를 바꿔 보세요.')
            : t('서버의 디렉토리를 추가하고 색인을 시작하면 사진과 영상을 여기서 찾을 수 있어요.')}
        </Empty>
      )}
    </section>
  );
}
