import { X } from './Icons';
import Taro from '@tarojs/taro';
import { Image } from '@tarojs/components';
import { imagePreviewPath } from '../lib/files';

export function ImageViewer({ src, alt, onClose }: { src: string; alt: string; onClose: () => void }) {
  return <div className="image-viewer" role="dialog" aria-modal="true" aria-label="查看图片" onClick={onClose}>
    <button type="button" className="image-viewer-close" aria-label="关闭图片" onClick={(event) => { event.stopPropagation(); onClose(); }}><X size={24} /></button>
    <Image className="image-viewer-image" src={src} mode="aspectFit" ariaLabel={alt} style={{ width: '100%', height: '100%' }} onClick={(event) => {
      event.stopPropagation();
      void imagePreviewPath(src).then((path) => Taro.previewImage({ current: path, urls: [path] })).catch(() => Taro.showToast({ title: '图片暂时无法打开', icon: 'none' }).catch(() => {}));
    }} />
  </div>;
}
