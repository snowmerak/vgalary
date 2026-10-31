import { useState, type FormEvent } from 'react';
import { Folder, FolderPlus, Play, RotateCcw, Trash2 } from 'lucide-react';
import { api, post } from './api';
import { Empty, Modal, Spinner } from './components';
import { DirectoryPicker } from './DirectoryPicker';
import { useActions } from './hooks';
import { date, sourceName } from './utils';
import type { Asset, Job, Source } from './types';

export function SourcesPage({
  sources,
  assets,
  jobs,
  refresh,
  onScan,
  notify,
}: {
  sources: Source[];
  assets: Asset[];
  jobs: Job[];
  refresh: () => void;
  onScan: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const [path, setPath] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const [remove, setRemove] = useState<Source | null>(null);
  const { run, pending } = useActions(notify);
  const add = (event: FormEvent) => {
    event.preventDefault();
    void run('add', async () => {
      await post('/sources', { root_path: path.trim() });
      setPath('');
      refresh();
      notify('디렉토리를 추가했습니다. 색인을 시작해 주세요.');
    });
  };
  const scan = (source: Source, force: boolean) =>
    void run(`${source.id}:scan`, async () => {
      await post(`/sources/${source.id}/scan`, { force });
      notify('색인 작업을 시작했습니다.');
      onScan();
    });
  return (
    <section>
      <div className="page-intro">
        <h1>디렉토리</h1>
        <p>검색할 미디어가 있는 디렉토리를 연결하고 관리하세요.</p>
      </div>
      <form className="add-source" onSubmit={add}>
        <label htmlFor="source-path">서버의 디렉토리 경로</label>
        <div>
          <Folder size={20} />
          <input
            id="source-path"
            placeholder="/home/me/Pictures 또는 D:/media"
            value={path}
            onChange={(event) => setPath(event.target.value)}
            required
          />
          <button type="button" className="secondary" onClick={() => setBrowsing(true)}>
            <Folder size={17} />
            찾아보기
          </button>
          <button className="primary" disabled={pending.has('add')}>
            {pending.has('add') ? <Spinner /> : <FolderPlus size={17} />}추가
          </button>
        </div>
        <p className="hint">
          앱이 실행되는 서버의 경로를 입력하세요. 하위 폴더와 중첩 ZIP·RAR도 함께 탐색합니다.
        </p>
      </form>
      <div className="section-heading">
        <h2>
          등록된 디렉토리 <span>{sources.length}</span>
        </h2>
      </div>
      {sources.length ? (
        <div className="source-list">
          {sources.map((source) => {
            const count = assets.filter((asset) => asset.source_id === source.id).length;
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
                    {active && <span className="status status-running">색인 중</span>}
                  </h3>
                  <p title={source.root_path}>{source.root_path}</p>
                  <span>
                    미디어 {count.toLocaleString()}개 · {date(source.created_at)} 등록
                  </span>
                </div>
                <div className="source-actions">
                  <button
                    className="secondary"
                    disabled={active || pending.has(`${source.id}:scan`)}
                    onClick={() => scan(source, false)}
                  >
                    <Play size={15} />
                    색인 시작
                  </button>
                  <button
                    className="icon-button"
                    title="내용을 다시 임베딩"
                    aria-label={`${sourceName(source)} 강제 재색인`}
                    disabled={active || pending.has(`${source.id}:scan`)}
                    onClick={() => scan(source, true)}
                  >
                    <RotateCcw size={17} />
                  </button>
                  <button
                    className="icon-button danger-text"
                    aria-label={`${sourceName(source)} 등록 제거`}
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
        <Empty icon={<FolderPlus size={32} />} title="아직 연결된 디렉토리가 없어요.">
          위에서 디렉토리를 추가해 첫 라이브러리를 만들어 보세요.
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
        <Modal title="디렉토리 등록을 제거할까요?" onClose={() => setRemove(null)} className="confirm-modal">
          <p className="confirm-path">{remove.root_path}</p>
          <p>
            이 디렉토리의 색인 작업을 중단하고 검색 목록에서 제외합니다. 원본 파일과 압축 파일은 그대로
            유지됩니다.
          </p>
          <div className="modal-actions">
            <button className="secondary" onClick={() => setRemove(null)}>
              취소
            </button>
            <button
              className="danger"
              disabled={pending.has('remove')}
              onClick={() =>
                void run('remove', async () => {
                  await api(`/sources/${remove.id}`, { method: 'DELETE' });
                  setRemove(null);
                  refresh();
                  notify('디렉토리 등록을 제거했습니다.');
                })
              }
            >
              {pending.has('remove') ? <Spinner /> : '등록 제거'}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
