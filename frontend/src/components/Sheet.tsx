import { X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';

/** H5 弹层的焦点恢复与键盘约束集中管理，迁移时可替换为小程序弹层。 */
export function Sheet({ title, subtitle, children, onClose }: { title: string; subtitle: string; children: ReactNode | ((close: () => void) => ReactNode); onClose: () => void }) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  const closingRef = useRef(false);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [visible, setVisible] = useState(false);
  const [closing, setClosing] = useState(false);
  closeRef.current = onClose;
  const requestClose = () => {
    if (closingRef.current) return;
    closingRef.current = true;
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) { closeRef.current(); return; }
    setClosing(true);
    setVisible(false);
    closeTimerRef.current = setTimeout(() => closeRef.current(), 380);
  };
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    const panel = panelRef.current!;
    panel.focus({ preventScroll: true });
    let openFrame = requestAnimationFrame(() => {
      openFrame = requestAnimationFrame(() => { if (!closingRef.current) setVisible(true); });
    });
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') requestClose();
      if (event.key === 'Tab') {
        const buttons = Array.from(panel.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex="0"]'));
        const first = buttons[0];
        const last = buttons[buttons.length - 1];
        if (event.shiftKey && (document.activeElement === first || document.activeElement === panel)) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === panel)) { event.preventDefault(); first?.focus(); }
      }
    };
    panel.addEventListener('keydown', onKey);
    return () => { cancelAnimationFrame(openFrame); panel.removeEventListener('keydown', onKey); if (closeTimerRef.current) clearTimeout(closeTimerRef.current); previousFocus?.focus({ preventScroll: true }); };
  }, []);
  return <div className={`sheet-backdrop ${visible ? 'is-open' : ''} ${closing ? 'is-closing' : ''}`} onClick={(event) => { if (event.target === event.currentTarget) requestClose(); }}><div className="sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} ref={panelRef} tabIndex={-1}><div className="sheet-handle" /><header className="sheet-header"><div><h2 id={titleId}>{title}</h2><p>{subtitle}</p></div><button className="icon-button" aria-label="关闭弹窗" onClick={requestClose}><X size={19} /></button></header>{typeof children === 'function' ? children(requestClose) : children}</div></div>;
}
