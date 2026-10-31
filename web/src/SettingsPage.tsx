import { useState, type FormEvent, type ReactNode } from 'react';
import { Check, Plug, Save } from 'lucide-react';
import { api, post } from './api';
import { Spinner } from './components';
import { useActions } from './hooks';
import type { Settings, SettingsEnvelope } from './types';

function Field({
  label,
  help,
  value,
  onChange,
  type = 'text',
  children,
}: {
  label: string;
  help?: string;
  value: string | number;
  onChange: (value: string) => void;
  type?: 'text' | 'url' | 'number';
  children?: ReactNode;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      <input type={type} value={value} onChange={(event) => onChange(event.target.value)} />
      {help && <small>{help}</small>}
      {children}
    </label>
  );
}
export function SettingsPage({
  envelope,
  refresh,
  notify,
}: {
  envelope: SettingsEnvelope;
  refresh: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const [form, setForm] = useState<Settings>(() => structuredClone(envelope.settings));
  const [dirty, setDirty] = useState(false);
  const [connection, setConnection] = useState('');
  const [models, setModels] = useState<string[]>([]);
  const { run, pending } = useActions(notify);
  const update = (group: keyof Settings, key: string, value: string | number | boolean) => {
    setForm((previous) => ({ ...previous, [group]: { ...previous[group], [key]: value } }));
    setDirty(true);
    setConnection('');
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void run('save', async () => {
      await api('/settings', { method: 'PUT', body: JSON.stringify(form) });
      setDirty(false);
      refresh();
      notify('설정을 저장했습니다. 새 작업부터 적용됩니다.');
    });
  };
  const test = () =>
    void run('test', async () => {
      const result = await post<{ dimensions: number; model: string }>('/connections/test');
      setConnection(`${result.model} · ${result.dimensions}차원 임베딩과 Qdrant 연결을 확인했습니다.`);
    });
  const loadModels = () =>
    void run('models', async () => {
      const result = await api<{ data: { id: string }[] }>('/models');
      setModels(result.data.map((model) => model.id));
    });
  return (
    <section className="settings-page">
      <div className="page-title-row">
        <div className="page-intro">
          <h1>설정</h1>
          <p>라이브러리의 연결과 미디어 처리 방식을 조정하세요.</p>
        </div>
        <button
          className="primary"
          form="settings-form"
          type="submit"
          disabled={pending.has('save') || !dirty}
        >
          {pending.has('save') ? <Spinner /> : <Save size={16} />}설정 저장
        </button>
      </div>
      <form id="settings-form" onSubmit={submit}>
        <fieldset disabled={pending.has('save')}>
          <section className="settings-section">
            <div className="settings-section-title">
              <h2>임베딩 연결</h2>
              <p>이미지와 검색어를 같은 공간으로 연결하는 모델입니다.</p>
            </div>
            <div className="field-grid">
              <Field
                label="서버 URL"
                value={form.embedding.base_url}
                onChange={(value) => update('embedding', 'base_url', value)}
                help="예: http://127.0.0.1:18182/v1"
              />
              <label className="field">
                <span>Model</span>
                <div className="input-with-action">
                  <input
                    value={form.embedding.model}
                    onChange={(event) => update('embedding', 'model', event.target.value)}
                    list="embedding-models"
                  />
                  <button
                    type="button"
                    className="text-button"
                    onClick={loadModels}
                    disabled={pending.has('models')}
                  >
                    {pending.has('models') ? <Spinner /> : '목록 조회'}
                  </button>
                </div>
                <datalist id="embedding-models">
                  {models.map((model) => (
                    <option key={model} value={model} />
                  ))}
                </datalist>
                <small>서버가 사용하는 모델 ID 또는 별칭</small>
              </label>
              <Field
                label="URL 환경 변수 이름 · 선택"
                value={form.embedding.base_url_env}
                onChange={(value) => update('embedding', 'base_url_env', value)}
                help="입력하면 저장한 URL 대신 해당 환경 값을 사용합니다."
              />
              <Field
                label="Model 환경 변수 이름 · 선택"
                value={form.embedding.model_env}
                onChange={(value) => update('embedding', 'model_env', value)}
              />
              <Field
                label="API Key 환경 변수 이름 · 선택"
                value={form.embedding.api_key_env}
                onChange={(value) => update('embedding', 'api_key_env', value)}
                help="키 값이 아닌 환경 변수 이름만 입력하세요. 비우면 인증 없이 연결합니다."
              />
              <Field
                label="모델 리비전"
                value={form.embedding.model_revision}
                onChange={(value) => update('embedding', 'model_revision', value)}
                help="같은 별칭의 가중치를 교체하면 변경해 주세요."
              />
              <div className="field-wide">
                <Field
                  label="검색 질의 프롬프트 접두사"
                  value={form.embedding.query_prefix}
                  onChange={(value) => update('embedding', 'query_prefix', value)}
                />
              </div>
            </div>
          </section>
          <section className="settings-section">
            <div className="settings-section-title">
              <h2>Qdrant 연결</h2>
              <p>미디어 벡터를 저장하고 유사한 장면을 찾습니다.</p>
            </div>
            <div className="field-grid">
              <Field
                label="서버 URL"
                value={form.qdrant.url}
                onChange={(value) => update('qdrant', 'url', value)}
                help="REST API 주소. 기본 포트는 6333입니다."
              />
              <Field
                label="컬렉션 접두사"
                value={form.qdrant.collection_prefix}
                onChange={(value) => update('qdrant', 'collection_prefix', value)}
              />
              <Field
                label="URL 환경 변수 이름 · 선택"
                value={form.qdrant.url_env}
                onChange={(value) => update('qdrant', 'url_env', value)}
              />
              <Field
                label="API Key 환경 변수 이름 · 선택"
                value={form.qdrant.api_key_env}
                onChange={(value) => update('qdrant', 'api_key_env', value)}
              />
            </div>
          </section>
          <section className="settings-section">
            <div className="settings-section-title">
              <h2>영상 프레임 선택</h2>
              <p>마지막으로 선택한 장면과 화면이 충분히 달라질 때만 임베딩합니다.</p>
            </div>
            <div className="threshold-field">
              <label htmlFor="frame-threshold">화면 변화 임계값</label>
              <div>
                <input
                  id="frame-threshold"
                  type="range"
                  min="0"
                  max="1"
                  step="0.01"
                  value={form.processing.video_frame_change_threshold}
                  onChange={(event) =>
                    update('processing', 'video_frame_change_threshold', Number(event.target.value))
                  }
                />
                <input
                  aria-label="화면 변화 임계값 숫자"
                  type="number"
                  min="0"
                  max="1"
                  step="0.01"
                  value={form.processing.video_frame_change_threshold}
                  onChange={(event) =>
                    update('processing', 'video_frame_change_threshold', Number(event.target.value))
                  }
                />
              </div>
              <p className="hint">
                0이면 모든 후보를 사용합니다. 높일수록 더 큰 화면 변화를 요구합니다. 변경 후 디렉토리를 다시
                색인해 주세요.
              </p>
            </div>
            <label className="checkbox-field">
              <input
                type="checkbox"
                checked={form.processing.allow_keyframe_fallback}
                onChange={(event) => update('processing', 'allow_keyframe_fallback', event.target.checked)}
              />
              <span>
                <strong>다른 코덱의 키프레임 대체 허용</strong>
                <small>AV1·VP9처럼 IDR이 없는 영상도 키프레임으로 색인합니다.</small>
              </span>
            </label>
          </section>
          <details className="advanced-settings">
            <summary>고급 처리 설정</summary>
            <div className="field-grid">
              <Field
                label="FFmpeg 실행 파일"
                value={form.processing.ffmpeg}
                onChange={(value) => update('processing', 'ffmpeg', value)}
              />
              <Field
                label="ffprobe 실행 파일"
                value={form.processing.ffprobe}
                onChange={(value) => update('processing', 'ffprobe', value)}
              />
              {(
                [
                  ['max_entry_bytes', '항목별 최대 바이트'],
                  ['max_expanded_bytes', '누적 압축 해제 바이트'],
                  ['max_entries', '최대 처리 항목 수'],
                  ['job_timeout_seconds', '작업 제한 시간 · 초'],
                  ['request_timeout_seconds', '외부 요청 제한 시간 · 초'],
                  ['process_timeout_seconds', '미디어 처리 제한 시간 · 초'],
                  ['image_max_dimension', '이미지 최대 변 길이 · px'],
                  ['max_image_pixels', '이미지 최대 픽셀 수'],
                ] as const
              ).map(([key, label]) => (
                <Field
                  key={key}
                  label={label}
                  type="number"
                  value={form.processing[key]}
                  onChange={(value) => update('processing', key, Number(value))}
                />
              ))}
            </div>
          </details>
        </fieldset>
      </form>
      <div className="connection-test">
        <div>
          <h2>연결 확인</h2>
          <p>저장된 설정으로 텍스트·테스트 이미지와 Qdrant를 확인합니다.</p>
        </div>
        <button className="secondary" type="button" disabled={dirty || pending.has('test')} onClick={test}>
          {pending.has('test') ? <Spinner /> : <Plug size={16} />}연결 확인
        </button>
      </div>
      {dirty && (
        <p className="hint">저장하지 않은 변경 사항이 있습니다. 연결 확인 전 설정을 저장해 주세요.</p>
      )}
      {connection && (
        <p className="connection-success" role="status">
          <Check size={17} />
          {connection}
        </p>
      )}
      {envelope.active_profile && (
        <p className="settings-footnote">
          검색은 현재 활성 색인을 사용합니다. 모델이나 프레임 선택 정책 변경은 등록된 디렉토리의 재색인이 끝난
          뒤 반영됩니다.
        </p>
      )}
    </section>
  );
}
