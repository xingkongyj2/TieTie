import { useEffect } from 'react';

/** Keep the app aligned with the visible viewport while mobile keyboards animate. */
export function useViewportHeight() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const root = document.documentElement;
    let frame = 0;
    let focusTimer: ReturnType<typeof setTimeout> | null = null;
    let composerFocusPending = false;
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
      const keyboardVisible = fullHeight - height > 40;
      if (keyboardVisible) composerFocusPending = false;
      const wasOpen = root.classList.contains('keyboard-open');
      root.classList.toggle('keyboard-open', composerFocusPending || (editable && fullHeight - height > 120) || (wasOpen && keyboardVisible));
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(update); };
    const onFocusIn = () => {
      const focused = document.activeElement;
      if (focused instanceof HTMLTextAreaElement && focused.closest('.composer-expanded-shell')) {
        composerFocusPending = true;
        if (focusTimer) clearTimeout(focusTimer);
        focusTimer = setTimeout(() => { composerFocusPending = false; focusTimer = null; update(); }, 700);
      }
      update();
    };
    const onFocusOut = () => {
      composerFocusPending = false;
      if (focusTimer) clearTimeout(focusTimer);
      focusTimer = null;
      update();
    };
    update();
    viewport.addEventListener('resize', schedule);
    viewport.addEventListener('scroll', schedule);
    window.addEventListener('resize', schedule);
    // Start collapsing the bottom navigation before the keyboard resizes the viewport.
    document.addEventListener('focusin', onFocusIn);
    document.addEventListener('focusout', onFocusOut);
    return () => {
      if (frame) cancelAnimationFrame(frame);
      if (focusTimer) clearTimeout(focusTimer);
      viewport.removeEventListener('resize', schedule);
      viewport.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      document.removeEventListener('focusin', onFocusIn);
      document.removeEventListener('focusout', onFocusOut);
      root.style.removeProperty('--app-height');
      root.style.removeProperty('--app-top');
      root.classList.remove('keyboard-open');
    };
  }, []);
}
