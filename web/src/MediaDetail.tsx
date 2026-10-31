import { useRef, useState } from 'react';
import { Archive, Copy, Download, Film, FolderOutput, Image as ImageIcon } from 'lucide-react';
import { post } from './api';
import { Modal, Spinner } from './components';
import { useActions } from './hooks';
import { bytes, filename, timestamp } from './utils';
import type { Result } from './types';

export function MediaDetail({
  result,
  onClose,
  notify,
}: {
  result: Result;
  onClose: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const { asset } = result;
  const [pointID, setPointID] = useState(result.point_id);
  const [failed, setFailed] = useState(false);
  const [exportDirectory, setExportDirectory] = useState('');
  const [exporting, setExporting] = useState(false);
  const { run, pending } = useActions(notify);
  const video = useRef<HTMLVideoElement>(null);
  const point = asset.points.find((item) => item.id === pointID) || asset.points[0];
  const archived = !!asset.location.archive_entries?.length;
  const isVideo = asset.media_type === 'video';
  const preview =
    archived || !isVideo
      ? `/api/assets/${asset.id}/${archived ? `thumbnail?point_id=${pointID}` : 'content'}`
      : '';
  const selectPoint = (id: string) => {
    setPointID(id);
    setFailed(false);
    const found = asset.points.find((item) => item.id === id);
    if (video.current && found?.timestamp_ms !== undefined)
      video.current.currentTime = found.timestamp_ms / 1000;
  };
  const copy = async () => {
    await navigator.clipboard.writeText(result.path);
    notify('원본 경로를 복사했습니다.');
  };
  return (
    <Modal title={filename(asset)} onClose={onClose} className="media-modal">
      <div className="detail-layout">
        <div className="detail-preview">
          {isVideo && !archived ? (
            <video
              ref={video}
              controls
              preload="metadata"
              src={`/api/assets/${asset.id}/content`}
              onLoadedMetadata={() => {
                if (video.current && point?.timestamp_ms !== undefined)
                  video.current.currentTime = point.timestamp_ms / 1000;
              }}
              onError={() => setFailed(true)}
            />
          ) : !failed ? (
            <img src={preview} alt={filename(asset)} onError={() => setFailed(true)} />
          ) : (
            <div className="media-unavailable">
              {isVideo ? <Film size={34} /> : <ImageIcon size={34} />}
              <span>미리보기를 사용할 수 없어요.</span>
            </div>
          )}
          {failed && isVideo && !archived && (
            <p>이 브라우저가 지원하지 않는 영상일 수 있어요. 원본을 다운로드해 주세요.</p>
          )}
        </div>
        <div className="detail-info">
          <span className="detail-kind">
            {archived && <Archive size={15} />}
            {isVideo ? '영상' : '이미지'}
            {archived ? ' · 압축 파일 내부' : ''}
          </span>
          <h3>{filename(asset)}</h3>
          <dl>
            <div>
              <dt>파일 크기</dt>
              <dd>{bytes(asset.size)}</dd>
            </div>
            {result.score !== undefined && (
              <div>
                <dt>검색 유사도</dt>
                <dd>{result.score.toFixed(4)}</dd>
              </div>
            )}
            {isVideo && (
              <>
                <div>
                  <dt>색인된 프레임</dt>
                  <dd>{asset.points.length}개</dd>
                </div>
                <div>
                  <dt>선택 시점</dt>
                  <dd className="mono">{timestamp(point?.timestamp_ms || 0)}</dd>
                </div>
              </>
            )}
          </dl>
          <div className="path-field">
            <label>원본 경로</label>
            <p>{result.path}</p>
            <button className="text-button" onClick={() => void run('copy', copy)}>
              <Copy size={14} />
              경로 복사
            </button>
          </div>
          {archived && (
            <p className="hint">
              압축 내부 항목은 저장된 미리보기만 표시합니다. 원본은 다운로드할 때 추출합니다.
            </p>
          )}
          {isVideo && (
            <div className="frame-section">
              <label>색인된 장면</label>
              <div className="frame-list">
                {asset.points.map((item) => (
                  <button
                    key={item.id}
                    className={item.id === pointID ? 'selected' : ''}
                    onClick={() => selectPoint(item.id)}
                  >
                    {timestamp(item.timestamp_ms || 0)}
                  </button>
                ))}
              </div>
            </div>
          )}
          <a className="primary download-button" href={`/api/assets/${asset.id}/download`}>
            <Download size={16} />
            원본 다운로드
          </a>
          <button className="secondary" onClick={() => setExporting((value) => !value)}>
            <FolderOutput size={16} />
            서버 디렉토리로 가져오기
          </button>
          {exporting && (
            <form
              className="export-form"
              onSubmit={(event) => {
                event.preventDefault();
                void run('export', async () => {
                  const response = await post<{ path: string }>(`/assets/${asset.id}/export`, {
                    directory: exportDirectory,
                  });
                  notify(`파일을 저장했습니다: ${response.path}`);
                  setExporting(false);
                });
              }}
            >
              <label htmlFor="export-directory">서버의 저장 디렉토리</label>
              <input
                id="export-directory"
                required
                placeholder="/home/me/exports"
                value={exportDirectory}
                onChange={(event) => setExportDirectory(event.target.value)}
              />
              <button className="primary" disabled={pending.has('export')}>
                {pending.has('export') ? <Spinner /> : '가져오기'}
              </button>
              <p className="hint">이미 존재하는 디렉토리를 입력해 주세요. 원본은 유지됩니다.</p>
            </form>
          )}
        </div>
      </div>
    </Modal>
  );
}
