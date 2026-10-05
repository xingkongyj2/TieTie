import { X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

/** H5 弹层的焦点恢复与键盘约束集中管理，迁移时可替换为小程序弹层。 */
export function Sheet({ title, children, onClose }: { title: string; children: ReactNode | ((close: () => void) => ReactNode); onClose: () => void }) {
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
    const backdrop = panel.parentElement!;
    const backdrops = Array.from(document.querySelectorAll<HTMLElement>('.sheet-backdrop'));
    const underneath = backdrops.slice(0, backdrops.indexOf(backdrop)).map((node) => ({ node, inert: node.inert, hidden: node.getAttribute('aria-hidden') }));
    underneath.forEach(({ node }) => { node.inert = true; node.setAttribute('aria-hidden', 'true'); });
    panel.focus({ preventScroll: true });
    let openFrame = requestAnimationFrame(() => {
      openFrame = requestAnimationFrame(() => { if (!closingRef.current) setVisible(true); });
    });
    const onKey = (event: KeyboardEvent) => {
      const dialogs = document.querySelectorAll('.sheet[role="dialog"]');
      if (dialogs[dialogs.length - 1] !== panel) return;
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); requestClose(); }
      if (event.key === 'Tab') {
        const buttons = Array.from(panel.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex="0"]'));
        const first = buttons[0];
        const last = buttons[buttons.length - 1];
        if (event.shiftKey && (document.activeElement === first || document.activeElement === panel)) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === panel)) { event.preventDefault(); first?.focus(); }
      }
    };
    panel.addEventListener('keydown', onKey);
    return () => {
      cancelAnimationFrame(openFrame);
      panel.removeEventListener('keydown', onKey);
      if (closeTimerRef.current) clearTimeout(closeTimerRef.current);
      underneath.forEach(({ node, inert, hidden }) => { node.inert = inert; if (hidden === null) node.removeAttribute('aria-hidden'); else node.setAttribute('aria-hidden', hidden); });
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
    };
  }, []);
  return createPortal(<div className={`sheet-backdrop ${visible ? 'is-open' : ''} ${closing ? 'is-closing' : ''}`} onClick={(event) => { if (event.target === event.currentTarget) requestClose(); }}><div className="sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} ref={panelRef} tabIndex={-1}><div className="sheet-handle" /><header className="sheet-header"><h2 id={titleId}>{title}</h2><button type="button" className="icon-button" aria-label="关闭弹窗" onClick={requestClose}><X size={19} /></button></header>{typeof children === 'function' ? children(requestClose) : children}</div></div>, document.querySelector('.app-shell') ?? document.body);
}
