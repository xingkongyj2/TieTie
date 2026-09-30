import type { Member } from '../types';

interface Props { member: Member; size?: 'tiny' | 'small' | 'normal' | 'large'; className?: string }

/** 同一份猫咪资产用于聊天、成员选择与提醒卡，避免重复维护头像样式。 */
export function Avatar({ member, size = 'normal', className = '' }: Props) {
  return <span className={`avatar avatar-${member.id} avatar-${size} ${className}`}>
    <img src={member.avatar} alt={`${member.name}的角色头像`} draggable={false} />
  </span>;
}
