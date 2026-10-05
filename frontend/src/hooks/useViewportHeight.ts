import { useEffect, useState } from 'react';
import Taro from '@tarojs/taro';

/** Native keyboard heights replace visualViewport and document-wide classes. */
export function useViewportHeight() {
  const [viewport, setViewport] = useState(() => ({ appHeight: Taro.getWindowInfo().windowHeight, keyboardOpen: false }));
  useEffect(() => {
    let fullHeight = Taro.getWindowInfo().windowHeight;
    let keyboardHeight = 0;
    const update = () => setViewport({ appHeight: Math.max(240, fullHeight - keyboardHeight), keyboardOpen: keyboardHeight > 40 });
    const onKeyboard = (event: { height: number }) => { keyboardHeight = event.height; update(); };
    const onResize = (event: { size?: { windowHeight: number }; errMsg?: string }) => {
      if (!event.size) return;
      // Android may shrink its window while the keyboard is showing. Keep the
      // full height so it is not subtracted a second time.
      if (!keyboardHeight || event.size.windowHeight > fullHeight) fullHeight = event.size.windowHeight;
      update();
    };
    const nativeKeyboard = process.env.TARO_ENV === 'weapp';
    if (nativeKeyboard) Taro.onKeyboardHeightChange(onKeyboard);
    Taro.onWindowResize(onResize);
    update();
    return () => {
      if (nativeKeyboard) Taro.offKeyboardHeightChange(onKeyboard);
      Taro.offWindowResize(onResize);
    };
  }, []);
  return viewport;
}
