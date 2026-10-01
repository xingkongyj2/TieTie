import type { Member } from '../types';
import { spaceBuddyArtwork } from '../data/ipCharacters';

interface Props { member: Member; size?: 'tiny' | 'small' | 'normal' | 'large'; className?: string }

/** AI 使用固定 IP；成员自选头像保留，同一组件用于聊天、成员选择与提醒卡。 */
export function Avatar({ member, size = 'normal', className = '' }: Props) {
  const isAI = member.id === 'ai';
  const src = isAI ? spaceBuddyArtwork.blue.src : member.avatar;
  return <span className={`avatar avatar-${member.id} avatar-${size} ${isAI ? 'avatar-ai-ip' : ''} ${className}`}>
    <img src={src} alt={`${member.name}的角色头像`} draggable={false} />
  </span>;
}
