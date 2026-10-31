import { useEffect, useRef, type ReactNode } from 'react';
import { Check, CircleAlert, LoaderCircle, X } from 'lucide-react';

export function Spinner() {
  return <LoaderCircle className="spin" size={18} aria-label="처리 중" />;
}
export function Empty({
  icon,
  title,
  children,
  action,
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <h2>{title}</h2>
      <p>{children}</p>
      {action}
    </div>
  );
}
export function ErrorBanner({ message, retry }: { message: string; retry?: () => void }) {
  return (
    <div className="error-banner" role="alert">
      <CircleAlert size={18} />
      <span>{message}</span>
      {retry && (
        <button className="text-button" onClick={retry}>
          다시 시도
        </button>
      )}
    </div>
  );
}
export function Modal({
  title,
  children,
  onClose,
  className = '',
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  className?: string;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog?.showModal();
    return () => {
      dialog?.close();
      if (previous?.isConnected) previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal ${className}`}
      aria-label={title}
      onKeyDown={(event) => {
        if (event.key !== 'Tab') return;
        const controls = Array.from(
          event.currentTarget.querySelectorAll<HTMLElement>(
            'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), video[controls], audio[controls], summary, [tabindex]',
          ),
        ).filter((element) => element.tabIndex >= 0 && element.getClientRects().length > 0);
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="modal-body">
        <header className="modal-header">
          <h2>{title}</h2>
          <button className="icon-button" aria-label="닫기" onClick={onClose}>
            <X size={20} />
          </button>
        </header>
        {children}
      </div>
    </dialog>
  );
}
export function Toast({ message, error, close }: { message: string; error?: boolean; close: () => void }) {
  return (
    <div className={`toast ${error ? 'toast-error' : ''}`} role={error ? 'alert' : 'status'}>
      {error ? <CircleAlert size={18} /> : <Check size={18} />}
      <span>{message}</span>
      <button onClick={close} className="icon-button" aria-label="알림 닫기">
        <X size={16} />
      </button>
    </div>
  );
}
