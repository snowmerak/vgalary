import { List, RefreshCw, RotateCcw, Square } from 'lucide-react';
import { post } from './api';
import { Empty, ErrorBanner } from './components';
import { useActions } from './hooks';
import { date, sourceName } from './utils';
import type { Job, Source } from './types';

const labels: Record<Job['status'], string> = {
  queued: '대기 중',
  running: '진행 중',
  succeeded: '완료',
  failed: '실패',
  canceled: '취소됨',
};
export function JobsPage({
  jobs,
  sources,
  error,
  refresh,
  notify,
}: {
  jobs: Job[];
  sources: Source[];
  error: string;
  refresh: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const { run, pending } = useActions(notify);
  const sorted = [...jobs].sort((a, b) => b.created_at.localeCompare(a.created_at));
  return (
    <section>
      <div className="page-title-row">
        <div className="page-intro">
          <h1>작업 현황</h1>
          <p>수집과 색인의 진행 상황을 확인하세요. 3초마다 새로 고침됩니다.</p>
        </div>
        <button className="secondary" onClick={refresh}>
          <RefreshCw size={16} />
          새로 고침
        </button>
      </div>
      {error && <ErrorBanner message={error} retry={refresh} />}
      {sorted.length ? (
        <div className="job-list">
          {sorted.map((job) => (
            <article className="job-row" key={job.id}>
              <header>
                <div>
                  <h2>
                    {sourceName(sources.find((source) => source.id === job.source_id))}
                    {!sources.some((source) => source.id === job.source_id) && <small>등록 제거됨</small>}
                  </h2>
                  <p>
                    {date(job.created_at)} 시작{job.finished_at && ` · ${date(job.finished_at)} 종료`}
                  </p>
                </div>
                <div className="job-controls">
                  <span className={`status status-${job.status}`}>{labels[job.status]}</span>
                  {['running', 'queued'].includes(job.status) ? (
                    <button
                      className="icon-button"
                      aria-label="작업 취소"
                      disabled={pending.has(job.id)}
                      onClick={() =>
                        void run(job.id, async () => {
                          await post(`/jobs/${job.id}/cancel`);
                          refresh();
                          notify('작업 취소를 요청했습니다.');
                        })
                      }
                    >
                      <Square size={16} />
                    </button>
                  ) : (
                    <button
                      className="icon-button"
                      aria-label="작업 다시 실행"
                      disabled={pending.has(job.id) || !sources.some((source) => source.id === job.source_id)}
                      onClick={() =>
                        void run(job.id, async () => {
                          await post(`/jobs/${job.id}/retry`);
                          refresh();
                          notify('새 작업을 등록했습니다.');
                        })
                      }
                    >
                      <RotateCcw size={17} />
                    </button>
                  )}
                </div>
              </header>
              <dl className="job-counters">
                {[
                  ['발견', job.discovered],
                  ['색인', job.indexed],
                  ['재사용', job.skipped],
                  ['임베딩 호출', job.embedded],
                  ['순환 건너뜀', job.cycles],
                  ['오류', job.issue_count],
                ].map(([label, value]) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>{Number(value).toLocaleString()}</dd>
                  </div>
                ))}
              </dl>
              {job.error && <p className="job-error">{job.error}</p>}
              {job.issues.length > 0 && (
                <details>
                  <summary>파일별 오류 {job.issue_count}개</summary>
                  <ul className="issue-list">
                    {job.issues.map((issue, index) => (
                      <li key={`${issue.location}:${index}`}>
                        <strong>{issue.location || '수집 루트'}</strong>
                        <span>{issue.message}</span>
                        <code>{issue.code}</code>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
            </article>
          ))}
        </div>
      ) : (
        <Empty icon={<List size={32} />} title="아직 실행한 작업이 없어요.">
          디렉토리에서 색인을 시작하면 진행 상황이 여기에 표시됩니다.
        </Empty>
      )}
    </section>
  );
}
