import { t, useLocale } from './i18n';
import { LanguagePicker } from './LanguagePicker';
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Folder, List, LockKeyhole, LogOut, Search, Settings as SettingsIcon } from 'lucide-react';
import { post } from './api';
import { ErrorBanner, Spinner, Toast } from './components';
import { useActions, useResource } from './hooks';
import { JobsPage } from './JobsPage';
import { MediaDetail } from './MediaDetail';
import { SearchPage } from './SearchPage';
import { SettingsPage } from './SettingsPage';
import { SourcesPage } from './SourcesPage';
import type { Job, Page, Result, Session, SettingsEnvelope } from './types';

const navigation = [
  { id: 'search', label: '검색', icon: Search },
  { id: 'sources', label: '디렉토리', icon: Folder },
  { id: 'jobs', label: '작업 현황', icon: List },
  { id: 'settings', label: '설정', icon: SettingsIcon },
] as const;
function currentPage(): Page {
  const value = window.location.hash.slice(1);
  return navigation.some((item) => item.id === value) ? (value as Page) : 'search';
}
function Brand() {
  return (
    <a href="#search" className="brand" aria-label={t('vgalary 홈')}>
      <span className="brand-symbol" aria-hidden="true" />
      <span>vgalary</span>
    </a>
  );
}

function Login({
  onSuccess,
  notify,
}: {
  onSuccess: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const [password, setPassword] = useState('');
  const { pending, run } = useActions(notify);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void run('login', async () => {
      await post('/login', { password });
      setPassword('');
      onSuccess();
    });
  };
  return (
    <main className="login-page">
      <Brand />
      <LanguagePicker />
      <div className="login-content">
        <LockKeyhole size={28} strokeWidth={1.5} />
        <h1>{t('나의 라이브러리에 들어가기')}</h1>
        <p>{t('계속하려면 서버에 설정한 비밀번호를 입력해 주세요.')}</p>
        <form onSubmit={submit}>
          <label className="field">
            <span>{t('비밀번호')}</span>
            <input
              autoFocus
              required
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button className="primary" disabled={pending.has('login')}>
            {pending.has('login') ? <Spinner /> : t('로그인')}
          </button>
        </form>
      </div>
    </main>
  );
}

function Library({
  session,
  logout,
  notify,
}: {
  session: Session;
  logout: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const settings = useResource<SettingsEnvelope>('/settings');
  const jobs = useResource<Job[]>('/jobs', 3000);
  const [page, setPage] = useState<Page>(currentPage);
  const [selected, setSelected] = useState<Result | null>(null);
  const previousJobs = useRef('');
  const refreshSettings = settings.refresh;
  useEffect(() => {
    const changed = () => setPage(currentPage());
    window.addEventListener('hashchange', changed);
    return () => window.removeEventListener('hashchange', changed);
  }, []);
  const jobSignature = JSON.stringify((jobs.data || []).map((job) => [job.id, job.status]));
  useEffect(() => {
    if (previousJobs.current && previousJobs.current !== jobSignature) {
      refreshSettings();
    }
    previousJobs.current = jobSignature;
  }, [jobSignature, refreshSettings]);
  const navigate = (value: Page) => {
    window.location.hash = value;
  };
  const refresh = () => {
    settings.refresh();
    jobs.refresh();
  };
  const sources = settings.data?.sources || [];
  const catalogKey = JSON.stringify([
    jobSignature,
    settings.data?.active_profile,
    sources.map((source) => source.id),
  ]);
  const connected = !settings.error && !!settings.data;
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <Brand />
        <nav aria-label={t('주 메뉴')}>
          {navigation.map((item) => (
            <a
              key={item.id}
              href={`#${item.id}`}
              className={page === item.id ? 'active' : ''}
              aria-current={page === item.id ? 'page' : undefined}
            >
              <item.icon size={21} strokeWidth={1.65} />
              <span>{t(item.label)}</span>
            </a>
          ))}
        </nav>
        <footer>
          <div className="library-status">
            <span className={`status-dot ${connected ? '' : 'offline'}`} />
            {connected ? t('라이브러리 연결됨') : t('연결 확인 중')}
          </div>
          {session.auth_required && (
            <button
              className="text-button"
              onClick={() =>
                void post('/logout')
                  .then(logout)
                  .catch((cause) => notify(String(cause), true))
              }
            >
              <LogOut size={15} />
              {t('로그아웃')}
            </button>
          )}
        </footer>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <div className="breadcrumb">
            {t('라이브러리')}
            <span>/</span> <strong>{t(navigation.find((item) => item.id === page)!.label)}</strong>
          </div>
          <div className="topbar-actions">
            <LanguagePicker />
            <button className="text-button manage-directories" onClick={() => navigate('sources')}>
              <Folder size={17} />
              {t('디렉토리 관리')}
            </button>
          </div>
        </header>
        <main className="page-content">
          {settings.error && <ErrorBanner message={settings.error} retry={settings.refresh} />}
          {settings.data?.configuration_error && (
            <ErrorBanner message={settings.data.configuration_error} retry={() => navigate('settings')} />
          )}
          {page === 'search' && (
            <SearchPage
              sources={sources}
              refreshKey={catalogKey}
              profileID={settings.data?.active_profile || ''}
              onSelect={setSelected}
              goSources={() => navigate('sources')}
            />
          )}
          {page === 'sources' && (
            <SourcesPage
              sources={sources}
              refreshKey={catalogKey}
              profileID={settings.data?.active_profile || ''}
              jobs={jobs.data || []}
              refresh={refresh}
              onScan={() => {
                jobs.refresh();
                navigate('jobs');
              }}
              notify={notify}
            />
          )}
          {page === 'jobs' && (
            <JobsPage
              jobs={jobs.data || []}
              sources={sources}
              error={jobs.error}
              refresh={jobs.refresh}
              notify={notify}
            />
          )}
          {page === 'settings' &&
            (settings.data ? (
              <SettingsPage envelope={settings.data} refresh={settings.refresh} notify={notify} />
            ) : (
              !settings.error && (
                <div className="loading">
                  <Spinner />
                  {t('설정을 불러오고 있어요.')}
                </div>
              )
            ))}
        </main>
      </div>
      {selected && (
        <MediaDetail
          key={selected.asset.id}
          result={selected}
          onClose={() => setSelected(null)}
          notify={notify}
        />
      )}
    </div>
  );
}

export default function App() {
  // The root subscribes so locale changes rerender all pages without resetting their state.
  const locale = useLocale();
  useEffect(() => {
    document.documentElement.lang = locale;
    document.title = `vgalary — ${t('나의 미디어 라이브러리')}`;
    document
      .querySelector('meta[name="description"]')
      ?.setAttribute(
        'content',
        t('폴더와 압축 파일 속 이미지와 영상을 자연어로 찾는 나만의 미디어 라이브러리'),
      );
  }, [locale]);
  const session = useResource<Session>('/session');
  const refreshSession = session.refresh;
  const [notice, setNotice] = useState<{ message: string; error?: boolean; id: number } | null>(null);
  const notify = useCallback(
    (message: string, error?: boolean) => setNotice({ message, error, id: performance.now() }),
    [],
  );
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(null), 7000);
    return () => window.clearTimeout(timer);
  }, [notice]);
  useEffect(() => {
    const expired = () => refreshSession();
    window.addEventListener('vgalary:unauthorized', expired);
    return () => window.removeEventListener('vgalary:unauthorized', expired);
  }, [refreshSession]);
  return (
    <>
      {session.data ? (
        session.data.authenticated ? (
          <Library session={session.data} logout={session.refresh} notify={notify} />
        ) : (
          <Login onSuccess={session.refresh} notify={notify} />
        )
      ) : (
        <main className="startup">
          <Brand />
          <LanguagePicker />
          {session.error ? (
            <ErrorBanner message={session.error} retry={session.refresh} />
          ) : (
            <div className="loading">
              <Spinner />
              {t('라이브러리를 연결하고 있어요.')}
            </div>
          )}
        </main>
      )}
      {notice && <Toast message={notice.message} error={notice.error} close={() => setNotice(null)} />}
    </>
  );
}
