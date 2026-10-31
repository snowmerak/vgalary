import { useEffect, useState, type FormEvent } from 'react';
import { ArrowUp, ChevronRight, Folder, Home, RefreshCw } from 'lucide-react';
import { api } from './api';
import { ErrorBanner, Modal, Spinner } from './components';

type DirectoryPage = {
  path: string;
  parent: string;
  home: string;
  roots: string[];
  entries: { name: string; path: string }[];
  total: number;
  offset: number;
  limit: number;
};

export function DirectoryPicker({
  initialPath,
  onSelect,
  onClose,
}: {
  initialPath: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState(initialPath);
  const [request, setRequest] = useState({ path: initialPath, offset: 0, revision: 0 });
  const [result, setResult] = useState<{ key: string; data?: DirectoryPage; error: string }>();
  const key = `/directories?${new URLSearchParams({ path: request.path, offset: String(request.offset), refresh: String(request.revision) })}`;
  const data = result?.data;
  const pending = result?.key !== key;
  const error = pending ? '' : result?.error || '';
  const ready = !pending && !error && !!data;
  const canSelect = ready && draft === data?.path;
  useEffect(() => {
    const controller = new AbortController();
    void api<DirectoryPage>(key, { signal: controller.signal })
      .then((page) => {
        if (!controller.signal.aborted) {
          setResult({ key, data: page, error: '' });
          setDraft(page.path);
        }
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted)
          setResult((previous) => ({
            key,
            data: previous?.data,
            error: cause instanceof Error ? cause.message : '폴더를 불러오지 못했습니다.',
          }));
      });
    return () => controller.abort();
  }, [key]);
  const navigate = (path: string, offset = 0) =>
    setRequest((previous) => ({ path, offset, revision: previous.revision + 1 }));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    navigate(draft);
  };
  return (
    <Modal title="서버 폴더 선택" onClose={onClose} className="directory-picker">
      <div className="picker-content">
        <p className="hint">앱이 실행되는 서버의 폴더를 탐색합니다. 원하는 폴더를 연 다음 선택해 주세요.</p>
        <label htmlFor="picker-path">폴더 경로</label>
        <form className="picker-path-form" onSubmit={submit}>
          <button
            type="button"
            className="icon-button"
            aria-label="상위 폴더로 이동"
            title="상위 폴더"
            disabled={pending || !data?.parent}
            onClick={() => data && navigate(data.parent)}
          >
            <ArrowUp size={18} />
          </button>
          <input
            id="picker-path"
            value={draft}
            disabled={pending}
            onChange={(event) => setDraft(event.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
          <button className="secondary" disabled={pending}>
            이동
          </button>
        </form>
        <div className="picker-shortcuts">
          <label htmlFor="picker-root">빠른 위치</label>
          <button
            type="button"
            className="icon-button"
            aria-label="홈 폴더로 이동"
            disabled={pending}
            onClick={() => navigate('')}
          >
            <Home size={18} />
          </button>
          <select
            id="picker-root"
            disabled={pending || !data}
            value={data?.roots.includes(data.path) ? data.path : ''}
            onChange={(event) => {
              if (event.target.value) navigate(event.target.value);
            }}
          >
            <option value="" disabled>
              홈 또는 드라이브 선택
            </option>
            {data?.roots.map((root) => (
              <option key={root} value={root}>
                {root === data.home ? '홈' : root}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="icon-button"
            aria-label="폴더 목록 새로 고침"
            disabled={pending}
            onClick={() => navigate(request.path, request.offset)}
          >
            <RefreshCw size={17} />
          </button>
        </div>
        <div className="picker-list" aria-label="하위 폴더" aria-busy={pending}>
          {pending ? (
            <div className="loading" role="status">
              <Spinner />
              폴더를 불러오고 있어요.
            </div>
          ) : error ? (
            <ErrorBanner message={error} retry={() => navigate(request.path, request.offset)} />
          ) : data?.entries.length ? (
            data.entries.map((entry) => (
              <button
                type="button"
                key={entry.path}
                className="picker-entry"
                aria-label={`${entry.name} 열기`}
                onClick={() => navigate(entry.path)}
              >
                <Folder size={19} />
                <span>{entry.name}</span>
                <ChevronRight size={16} />
              </button>
            ))
          ) : (
            <p className="picker-empty">하위 폴더가 없습니다. 이 폴더를 선택할 수 있어요.</p>
          )}
        </div>
        {ready && data && (
          <div className="picker-pagination">
            <span>하위 폴더 {data.total.toLocaleString()}개</span>
            {data.total > data.limit && (
              <div>
                <button
                  type="button"
                  className="text-button"
                  disabled={data.offset === 0}
                  onClick={() => navigate(data.path, Math.max(0, data.offset - data.limit))}
                >
                  이전
                </button>
                <button
                  type="button"
                  className="text-button"
                  disabled={data.offset + data.limit >= data.total}
                  onClick={() => navigate(data.path, data.offset + data.limit)}
                >
                  다음
                </button>
              </div>
            )}
          </div>
        )}
      </div>
      <footer className="picker-footer">
        <p title={ready ? data?.path : undefined}>
          {ready && !canSelect
            ? '변경한 경로는 이동 버튼이나 Enter로 확인해 주세요.'
            : ready
              ? data?.path
              : error
                ? '경로를 확인하거나 다른 위치로 이동해 주세요.'
                : '선택할 폴더를 확인하고 있어요.'}
        </p>
        <div>
          <button type="button" className="secondary" onClick={onClose}>
            취소
          </button>
          <button
            type="button"
            className="primary"
            disabled={!canSelect}
            onClick={() => {
              if (canSelect && data) onSelect(data.path);
            }}
          >
            이 폴더 선택
          </button>
        </div>
      </footer>
    </Modal>
  );
}
