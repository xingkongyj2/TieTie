import { useEffect } from 'react';

/**
 * iOS 的软键盘不会始终改变 CSS 布局视口。按可视视口收缩聊天容器，
 * 让输入栏留在键盘上方；此 H5 专用适配可在迁移 Taro 时移除。
 */
export function useViewportHeight() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const update = () => {
      document.documentElement.style.setProperty('--app-height', `${viewport.height}px`);
    };
    update();
    viewport.addEventListener('resize', update);
    return () => {
      viewport.removeEventListener('resize', update);
      document.documentElement.style.removeProperty('--app-height');
    };
  }, []);
}
