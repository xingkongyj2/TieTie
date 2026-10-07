import { request } from './client'
import type { Member, RegionLocation } from '../types'
import { readWechatAvatar } from '../lib/wechat-avatar'

export interface ProvinceOption {
  code: string
  name: string
  codeSystem: string
  cities: { code: string; name: string; districts: { code: string; name: string }[] }[]
}

export interface UserProfile {
  userId: number
  name: string
  gender: NonNullable<Member['gender']>
  birthday: string
  hobbies: string[]
  bio: string
  avatar: string
  region?: RegionLocation | null
}

export const profileApi = {
  async uploadAvatar(tempPath: string): Promise<string> {
    const avatarBase64 = await readWechatAvatar(tempPath)
    const result = await request<{ avatar: string }>('/api/account/avatar', { method: 'POST', body: { avatarBase64 } })
    if (!/^\/api\/assets\/avatars\/[a-f0-9]{32}$/.test(result.avatar)) throw new Error('头像保存失败，请重试。')
    return result.avatar
  },
  regions(): Promise<{ version: string; provinces: ProvinceOption[] }> {
    return request('/api/account/regions')
  },
  get(): Promise<{ profiles: UserProfile[] }> {
    return request('/api/account/profiles')
  },
  save(member: Member): Promise<{ profile: UserProfile; memoryStatus: 'pending' | 'not_bound' }> {
    return request('/api/account/profile', { method: 'PUT', body: {
      name: member.name,
      gender: member.gender ?? 'unspecified', birthday: member.birthday,
      hobbies: member.hobbies, bio: member.bio, avatar: member.avatar,
      region: member.region ?? null,
    } })
  },
}
