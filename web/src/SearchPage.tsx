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
      aria-label={`${filename(asset)} 자세히 보기`}
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
            <span>미리보기 없음</span>
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
          자세히 보기
        </span>
      </div>
      <div className="media-caption">
        <span className="media-name">
          {archived && <Archive size={14} />}
          {filename(asset)}
        </span>
        {result.score !== undefined && (
          <span className="media-score" title="Cosine 유사도">
            {result.score.toFixed(3)}
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
  assets,
  sources,
  error,
  refresh,
  onSelect,
  goSources,
}: {
  assets?: Asset[];
  sources: Source[];
  error: string;
  refresh: () => void;
  onSelect: (result: Result) => void;
  goSources: () => void;
}) {
  const [query, setQuery] = useState('');
  const [submitted, setSubmitted] = useState('');
  const [kind, setKind] = useState<MediaType>('');
  const [source, setSource] = useState('');
  const [results, setResults] = useState<Result[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [searchError, setSearchError] = useState('');
  const [visible, setVisible] = useState(24);
  const [hasMore, setHasMore] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const request = useRef<AbortController | null>(null);
  const intent = useRef('');
  useEffect(() => {
    const focus = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener('keydown', focus);
    return () => {
      window.removeEventListener('keydown', focus);
      request.current?.abort();
    };
  }, []);
  const search = async (text: string, nextKind = kind, nextSource = source, append = false) => {
    intent.current = text.trim();
    if (!text.trim()) {
      request.current?.abort();
      setBusy(false);
      setResults(null);
      setSubmitted('');
      setSearchError('');
      return;
    }
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setSearchError('');
    try {
      const response = await api<{ results: Result[] }>('/search', {
        method: 'POST',
        body: JSON.stringify({
          query: text.trim(),
          media_type: nextKind,
          source_id: nextSource,
          limit: 24,
          offset: append ? results?.length || 0 : 0,
        }),
        signal: controller.signal,
      });
      if (!controller.signal.aborted) {
        setResults((previous) => (append ? [...(previous || []), ...response.results] : response.results));
        setHasMore(response.results.length === 24);
        setSubmitted(text.trim());
      }
    } catch (cause) {
      if (!controller.signal.aborted)
        setSearchError(cause instanceof Error ? cause.message : '검색하지 못했습니다.');
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void search(query);
  };
  const filtered = (assets || [])
    .filter((asset) => (!kind || asset.media_type === kind) && (!source || asset.source_id === source))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  const shown = results ?? filtered.slice(0, visible).map((asset) => libraryResult(asset, sources));
  const changeKind = (value: MediaType) => {
    setKind(value);
    setVisible(24);
    if (intent.current) void search(intent.current, value, source);
  };
  const changeSource = (value: string) => {
    setSource(value);
    setVisible(24);
    if (intent.current) void search(intent.current, kind, value);
  };
  return (
    <section className="search-page">
      <div className="page-intro">
        <h1>기억나는 장면을 찾아보세요.</h1>
        <p>이미지와 영상, 압축 파일 속까지 자연어로 검색하세요.</p>
      </div>
      <form className="search-box" onSubmit={submit}>
        <Search size={24} strokeWidth={1.7} />
        <input
          ref={input}
          aria-label="자연어 검색"
          placeholder="파란 고양이 장난감"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <kbd>⌘ K</kbd>
        <button className="primary" type="submit" disabled={busy}>
          {busy ? <Spinner /> : '검색'}
        </button>
      </form>
      <div className="filters">
        <div className="segmented" aria-label="미디어 종류">
          {(
            [
              ['', '전체'],
              ['image', '이미지'],
              ['video', '영상'],
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
            aria-label="검색 디렉토리"
            value={source}
            onChange={(event) => changeSource(event.target.value)}
          >
            <option value="">모든 디렉토리</option>
            {sources.map((item) => (
              <option key={item.id} value={item.id}>
                {sourceName(item)}
              </option>
            ))}
          </select>
          <ChevronDown size={16} />
        </div>
      </div>
      {error && <ErrorBanner message={error} retry={refresh} />}
      {searchError && <ErrorBanner message={searchError} retry={() => void search(query)} />}
      <div className="results-heading">
        <h2>
          {submitted ? '검색 결과' : '라이브러리'}
          {submitted && <span>“{submitted}”</span>}
        </h2>
        <span>
          {submitted ? '관련도순' : '최근 색인순'}
          <ChevronDown size={14} />
        </span>
      </div>
      {assets === undefined && !results && !error ? (
        <div className="loading">
          <Spinner />
          라이브러리를 불러오고 있어요.
        </div>
      ) : shown.length > 0 ? (
        <>
          <div className={`gallery ${busy ? 'gallery-updating' : ''}`}>
            {shown.map((result) => (
              <MediaCard key={`${result.asset.id}:${result.point_id}`} result={result} onSelect={onSelect} />
            ))}
          </div>
          {((results && hasMore) || (!results && filtered.length > visible)) && (
            <div className="load-more">
              <button
                className="secondary"
                disabled={busy}
                onClick={() =>
                  results ? void search(submitted, kind, source, true) : setVisible((value) => value + 24)
                }
              >
                {busy ? <Spinner /> : '더 보기'}
              </button>
            </div>
          )}
        </>
      ) : (
        <Empty
          icon={submitted ? <Search size={30} /> : <Sparkles size={30} />}
          title={submitted ? '일치하는 미디어가 없어요.' : '나만의 라이브러리를 연결해 보세요.'}
          action={
            !submitted && (
              <button className="primary" onClick={goSources}>
                디렉토리 추가
                <ArrowUpRight size={16} />
              </button>
            )
          }
        >
          {submitted
            ? '다른 표현을 사용하거나 미디어 종류와 디렉토리 필터를 바꿔 보세요.'
            : '서버의 디렉토리를 추가하고 색인을 시작하면 사진과 영상을 여기서 찾을 수 있어요.'}
        </Empty>
      )}
    </section>
  );
}
