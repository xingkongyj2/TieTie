import { request } from './client'
import type { Member } from '../types'

export interface UserProfile {
  userId: number
  gender: NonNullable<Member['gender']>
  birthday: string
  hobbies: string[]
  bio: string
  avatar: string
}

export const profileApi = {
  get(): Promise<{ profiles: UserProfile[] }> {
    return request('/api/account/profiles')
  },
  save(member: Member): Promise<{ profile: UserProfile; memoryStatus: 'pending' | 'not_bound' }> {
    return request('/api/account/profile', { method: 'PUT', body: {
      gender: member.gender ?? 'unspecified', birthday: member.birthday,
      hobbies: member.hobbies, bio: member.bio, avatar: member.avatar,
    } })
  },
}
