import { t, number } from './i18n';
import { useState, type FormEvent } from 'react';
import { Folder, FolderPlus, Play, RotateCcw, Trash2 } from 'lucide-react';
import { api, post } from './api';
import { Empty, ErrorBanner, Modal, Spinner } from './components';
import { DirectoryPicker } from './DirectoryPicker';
import { useActions, useResource } from './hooks';
import { date, sourceName } from './utils';
import type { Job, Source } from './types';

export function SourcesPage({
  sources,
  refreshKey,
  profileID,
  jobs,
  refresh,
  onScan,
  notify,
}: {
  sources: Source[];
  refreshKey: string;
  profileID: string;
  jobs: Job[];
  refresh: () => void;
  onScan: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const [path, setPath] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const [remove, setRemove] = useState<Source | null>(null);
  const { run, pending } = useActions(notify);
  const counts = useResource<Record<string, number>>(
    `/assets/counts?${new URLSearchParams({ profile_id: profileID })}`,
    0,
    refreshKey,
  );
  const add = (event: FormEvent) => {
    event.preventDefault();
    void run('add', async () => {
      await post('/sources', { root_path: path.trim() });
      setPath('');
      refresh();
      counts.refresh();
      notify(t('디렉토리를 추가했습니다. 색인을 시작해 주세요.'));
    });
  };
  const scan = (source: Source, force: boolean) =>
    void run(`${source.id}:scan`, async () => {
      await post(`/sources/${source.id}/scan`, { force });
      notify(t('색인 작업을 시작했습니다.'));
      onScan();
    });
  return (
    <section>
      <div className="page-intro">
        <h1>{t('디렉토리')}</h1>
        <p>{t('검색할 미디어가 있는 디렉토리를 연결하고 관리하세요.')}</p>
      </div>
      <form className="add-source" onSubmit={add}>
        <label htmlFor="source-path">{t('서버의 디렉토리 경로')}</label>
        <div>
          <Folder size={20} />
          <input
            id="source-path"
            placeholder={t('/home/me/Pictures 또는 D:/media')}
            value={path}
            onChange={(event) => setPath(event.target.value)}
            required
          />
          <button type="button" className="secondary" onClick={() => setBrowsing(true)}>
            <Folder size={17} />
            {t('찾아보기')}
          </button>
          <button className="primary" disabled={pending.has('add')}>
            {pending.has('add') ? <Spinner /> : <FolderPlus size={17} />}
            {t('추가')}
          </button>
        </div>
        <p className="hint">
          {t('앱이 실행되는 서버의 경로를 입력하세요. 하위 폴더와 중첩 ZIP·RAR도 함께 탐색합니다.')}
        </p>
      </form>
      <div className="section-heading">
        <h2>
          {t('등록된 디렉토리')} <span>{number(sources.length)}</span>
        </h2>
      </div>
      {counts.error && <ErrorBanner message={counts.error} retry={counts.refresh} />}
      {sources.length ? (
        <div className="source-list">
          {sources.map((source) => {
            const count = counts.data?.[source.id];
            const active = jobs.some(
              (job) => job.source_id === source.id && ['queued', 'running'].includes(job.status),
            );
            return (
              <div className="source-row" key={source.id}>
                <div className="source-icon">
                  <Folder size={25} />
                </div>
                <div className="source-description">
                  <h3>
                    {sourceName(source)}
                    {active && <span className="status status-running">{t('색인 중')}</span>}
                  </h3>
                  <p title={source.root_path}>{source.root_path}</p>
                  <span>
                    {counts.data
                      ? t('미디어 {count}개', { count: count ?? 0 })
                      : counts.error
                        ? t('미디어 개수 확인 실패')
                        : t('미디어 집계 중')}{' '}
                    · {t('{date} 등록', { date: date(source.created_at) })}
                  </span>
                </div>
                <div className="source-actions">
                  <button
                    className="secondary"
                    disabled={active || pending.has(`${source.id}:scan`)}
                    onClick={() => scan(source, false)}
                  >
                    <Play size={15} />
                    {t('색인 시작')}
                  </button>
                  <button
                    className="icon-button"
                    title={t('내용을 다시 임베딩')}
                    aria-label={t('{name} 강제 재색인', { name: sourceName(source) })}
                    disabled={active || pending.has(`${source.id}:scan`)}
                    onClick={() => scan(source, true)}
                  >
                    <RotateCcw size={17} />
                  </button>
                  <button
                    className="icon-button danger-text"
                    aria-label={t('{name} 등록 제거', { name: sourceName(source) })}
                    onClick={() => setRemove(source)}
                  >
                    <Trash2 size={17} />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <Empty icon={<FolderPlus size={32} />} title={t('아직 연결된 디렉토리가 없어요.')}>
          {t('위에서 디렉토리를 추가해 첫 라이브러리를 만들어 보세요.')}
        </Empty>
      )}
      {browsing && (
        <DirectoryPicker
          initialPath={path}
          onClose={() => setBrowsing(false)}
          onSelect={(selected) => {
            setPath(selected);
            setBrowsing(false);
          }}
        />
      )}
      {remove && (
        <Modal
          title={t('디렉토리 등록을 제거할까요?')}
          onClose={() => setRemove(null)}
          className="confirm-modal"
        >
          <p className="confirm-path">{remove.root_path}</p>
          <p>
            {t(
              '이 디렉토리의 색인 작업을 중단하고 검색 목록에서 제외합니다. 원본 파일과 압축 파일은 그대로 유지됩니다.',
            )}
          </p>
          <div className="modal-actions">
            <button className="secondary" onClick={() => setRemove(null)}>
              {t('취소')}
            </button>
            <button
              className="danger"
              disabled={pending.has('remove')}
              onClick={() =>
                void run('remove', async () => {
                  await api(`/sources/${remove.id}`, { method: 'DELETE' });
                  setRemove(null);
                  refresh();
                  counts.refresh();
                  notify(t('디렉토리 등록을 제거했습니다.'));
                })
              }
            >
              {pending.has('remove') ? <Spinner /> : t('등록 제거')}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
