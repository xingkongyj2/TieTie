import { Image } from '@tarojs/components';
import type { Member } from '../types';
import { spaceBuddyArtwork } from '../data/ipCharacters';
import { assetUrl } from '../lib/assets';

interface Props { member: Member; size?: 'tiny' | 'small' | 'normal' | 'large'; className?: string; showAILabel?: boolean }

/** AI 使用固定 IP；成员使用登录头像，同一组件用于聊天、成员选择与提醒卡。 */
export function Avatar({ member, size = 'normal', className = '', showAILabel = false }: Props) {
  const isAI = member.id === 'ai';
  const fallback = member.id === 'partner' ? '/avatars/peach-cat.png' : '/avatars/cream-cat.png';
  const src = isAI ? spaceBuddyArtwork.blue.src : member.avatar || fallback;
  return <span className={`avatar avatar-${member.id} avatar-${size} ${isAI ? 'avatar-ai-ip' : ''} ${className}`}>
    <Image className="h5-img" src={assetUrl(src)} mode="aspectFit" ariaLabel={`${member.name}的头像`} style={{ width: '100%', height: '100%' }} />
    {isAI && showAILabel && <span className="avatar-ai-label" aria-label="人工智能助手">AI</span>}
  </span>;
}
