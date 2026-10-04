import { useEffect } from 'react';

/** Keep the app aligned with the visible viewport while mobile keyboards animate. */
export function useViewportHeight() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const root = document.documentElement;
    let frame = 0;
    let fullHeight = Math.max(window.innerHeight, viewport.height);
    const update = () => {
      frame = 0;
      const top = Math.floor(viewport.offsetTop);
      const height = Math.ceil(viewport.height + viewport.offsetTop - top);
      const focused = document.activeElement;
      const editable = focused instanceof HTMLInputElement || focused instanceof HTMLTextAreaElement;
      if (!editable || height > fullHeight - 80) fullHeight = Math.max(fullHeight, height);
      root.style.setProperty('--app-height', `${height}px`);
      root.style.setProperty('--app-top', `${top}px`);
      root.classList.toggle('keyboard-open', editable && fullHeight - height > 120);
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(update); };
    update();
    viewport.addEventListener('resize', schedule);
    viewport.addEventListener('scroll', schedule);
    window.addEventListener('resize', schedule);
    document.addEventListener('focusin', schedule);
    document.addEventListener('focusout', schedule);
    return () => {
      if (frame) cancelAnimationFrame(frame);
      viewport.removeEventListener('resize', schedule);
      viewport.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      document.removeEventListener('focusin', schedule);
      document.removeEventListener('focusout', schedule);
      root.style.removeProperty('--app-height');
      root.style.removeProperty('--app-top');
      root.classList.remove('keyboard-open');
    };
  }, []);
}
