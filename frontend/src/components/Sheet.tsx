import { X } from './Icons';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import Taro from '@tarojs/taro';
import { ScrollView, View } from '@tarojs/components';
import { nextFrame, cancelFrame, isAppVisible } from '../lib/platform';

/** In-tree native sheet, using the same backdrop and slide transition classes. */
export function Sheet({ title, children, onClose }: { title: string; children: ReactNode | ((close: () => void) => ReactNode); onClose: () => void }) {
  const titleId = useId().replace(/:/g, '');
  const closeRef = useRef(onClose);
  const closingRef = useRef(false);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [visible, setVisible] = useState(false);
  const [closing, setClosing] = useState(false);
  const [height, setHeight] = useState<number | undefined>();
  closeRef.current = onClose;
  const requestClose = () => {
    if (closingRef.current) return;
    closingRef.current = true;
    setClosing(true); setVisible(false);
    closeTimerRef.current = setTimeout(() => closeRef.current(), 380);
  };
  useEffect(() => {
    let alive = true;
    void Taro.hideKeyboard().catch(() => {});
    const measure = () => {
      if (!alive || closingRef.current || !isAppVisible()) return;
      Taro.createSelectorQuery().select(`#${titleId}-content`).boundingClientRect((rect) => {
        const bounds = Array.isArray(rect) ? rect[0] : rect;
        if (!alive || closingRef.current || !bounds) return;
        const info = Taro.getWindowInfo();
        const bottomInset = info.safeArea ? info.screenHeight - info.safeArea.bottom : 0;
        setHeight(Math.min(bounds.height + 10 + Math.max(23, bottomInset), info.windowHeight * .91));
      }).exec();
    };
    let frame = nextFrame(() => {
      measure();
      frame = nextFrame(() => { if (alive && !closingRef.current) setVisible(true); });
    });
    // Native views have no ResizeObserver. Measuring this single open panel
    // also accommodates delayed server data and children expanding in place.
    const layoutTimer = setInterval(measure, 500);
    return () => { alive = false; clearInterval(layoutTimer); cancelFrame(frame); if (closeTimerRef.current) clearTimeout(closeTimerRef.current); };
  }, []);
  return <div className={`sheet-backdrop ${visible ? 'is-open' : ''} ${closing ? 'is-closing' : ''}`} onClick={(event) => {
    if (process.env.TARO_ENV !== 'h5' || event.target === event.currentTarget) requestClose();
  }}>
    <ScrollView className="sheet" scrollY enhanced showScrollbar={false} style={height ? { height: `${height}px` } : {}} onClick={process.env.TARO_ENV === 'h5' ? undefined : (event) => event.stopPropagation()}>
      <View id={`${titleId}-content`}>
        <div className="sheet-handle" />
        <header className="sheet-header"><h2 id={titleId}>{title}</h2><button type="button" className="icon-button" aria-label="关闭弹窗" onClick={requestClose}><X size={19} /></button></header>
        {typeof children === 'function' ? children(requestClose) : children}
      </View>
    </ScrollView>
  </div>;
}
