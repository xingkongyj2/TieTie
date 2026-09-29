import { X } from 'lucide-react';
import { useEffect, useRef } from 'react';

export function ImageViewer({ src, alt, onClose }: { src: string; alt: string; onClose: () => void }) {
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    closeButtonRef.current?.focus({ preventScroll: true });
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCloseRef.current();
      if (event.key === 'Tab') { event.preventDefault(); closeButtonRef.current?.focus({ preventScroll: true }); }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => { document.removeEventListener('keydown', onKeyDown); previousFocus?.focus({ preventScroll: true }); };
  }, []);

  return <div className="image-viewer" role="dialog" aria-modal="true" aria-label="查看图片" onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <button ref={closeButtonRef} type="button" className="image-viewer-close" aria-label="关闭图片" onClick={onClose}><X size={24} /></button>
    <img src={src} alt={alt} onClick={onClose} />
  </div>;
}
