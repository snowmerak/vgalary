import { t, number } from './i18n';
import type { MessageKey } from './messages';
import { List, RefreshCw, RotateCcw, Square } from 'lucide-react';
import { post } from './api';
import { Empty, ErrorBanner } from './components';
import { useActions } from './hooks';
import { date, sourceName } from './utils';
import type { Job, Source } from './types';

const labels: Record<Job['status'], MessageKey> = {
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
          <h1>{t('작업 현황')}</h1>
          <p>{t('수집과 색인의 진행 상황을 확인하세요. 3초마다 새로 고침됩니다.')}</p>
        </div>
        <button className="secondary" onClick={refresh}>
          <RefreshCw size={16} />
          {t('새로 고침')}
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
                    {!sources.some((source) => source.id === job.source_id) && (
                      <small>{t('등록 제거됨')}</small>
                    )}
                  </h2>
                  <p>
                    {t('{date} 시작', { date: date(job.created_at) })}
                    {job.finished_at && ` · ${t('{date} 종료', { date: date(job.finished_at) })}`}
                  </p>
                </div>
                <div className="job-controls">
                  <span className={`status status-${job.status}`}>{t(labels[job.status])}</span>
                  {['running', 'queued'].includes(job.status) ? (
                    <button
                      className="icon-button"
                      aria-label={t('작업 취소')}
                      disabled={pending.has(job.id)}
                      onClick={() =>
                        void run(job.id, async () => {
                          await post(`/jobs/${job.id}/cancel`);
                          refresh();
                          notify(t('작업 취소를 요청했습니다.'));
                        })
                      }
                    >
                      <Square size={16} />
                    </button>
                  ) : (
                    <button
                      className="icon-button"
                      aria-label={t('작업 다시 실행')}
                      disabled={pending.has(job.id) || !sources.some((source) => source.id === job.source_id)}
                      onClick={() =>
                        void run(job.id, async () => {
                          await post(`/jobs/${job.id}/retry`);
                          refresh();
                          notify(t('새 작업을 등록했습니다.'));
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
                  [t('발견'), job.discovered],
                  [t('색인'), job.indexed],
                  [t('재사용'), job.skipped],
                  [t('임베딩 호출'), job.embedded],
                  [t('순환 건너뜀'), job.cycles],
                  [t('오류'), job.issue_count],
                ].map(([label, value]) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>{number(Number(value))}</dd>
                  </div>
                ))}
              </dl>
              {job.error && <p className="job-error">{job.error}</p>}
              {job.issues.length > 0 && (
                <details>
                  <summary>{t('파일별 오류 {count}개', { count: job.issue_count })}</summary>
                  <ul className="issue-list">
                    {job.issues.map((issue, index) => (
                      <li key={`${issue.location}:${index}`}>
                        <strong>{issue.location || t('수집 루트')}</strong>
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
        <Empty icon={<List size={32} />} title={t('아직 실행한 작업이 없어요.')}>
          {t('디렉토리에서 색인을 시작하면 진행 상황이 여기에 표시됩니다.')}
        </Empty>
      )}
    </section>
  );
}
