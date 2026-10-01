import type { RelationshipState } from '../types'
import { spaceBuddyArtwork } from './ipCharacters'

/** Demo only: no real AI, weather service, payment, or notification is connected. */
export const LOCAL_MOCK_NOTICE = '本地体验版 · 对话为模拟回复，数据仅保存在当前设备'

export const initialRelationshipState: RelationshipState = {
  togetherSince: '2026-05-24',
  members: [
    {
      id: 'ai',
      name: '贴贴',
      role: '你们的 AI 小管家',
      avatar: spaceBuddyArtwork.blue.src,
      birthday: '',
      hobbies: ['记住小事', '好好说话', '偷偷助攻'],
      bio: '你们负责相爱，小事交给我记着 ฅ^•ﻌ•^ฅ',
    },
    {
      id: 'self',
      name: '阿言',
      role: '我',
      avatar: '/avatars/cream-cat.png',
      gender: 'unspecified',
      birthday: '1998-11-16',
      hobbies: ['做饭', '拍照', '陪小满散步'],
      bio: '嘴上说随便，其实最想和你一起吃饭 🍳',
    },
    {
      id: 'partner',
      name: '小满',
      role: '另一半',
      avatar: '/avatars/peach-cat.png',
      gender: 'unspecified',
      birthday: '1999-06-08',
      hobbies: ['奶茶三分糖', '小猫', '周末探店'],
      bio: '今天也要收集一点点开心！( •̀ ω •́ )✧',
    },
  ],
  settings: {
    name: '贴贴',
    tone: 'playful',
    sharedReminders: true,
    weatherCare: false,
    anniversaryReminders: true,
    quietHours: true,
  },
  messages: [
    {
      id: 'message-dinner',
      sender: 'partner',
      text: '今晚想吃你做的番茄牛腩～\n大厨今天营业吗？🥺',
      time: '18:30',
    },
    {
      id: 'message-promise',
      sender: 'self',
      text: '营业！只接小满这一桌 😎\n@贴贴 19:00 提醒我们买番茄 🍅',
      time: '18:31',
    },
    {
      id: 'message-reminder',
      sender: 'ai',
      text: '收到，记进我们的小本本啦！\n你们负责甜，我负责记 🐾',
      time: '18:32',
      kind: 'reminder',
      reminderId: 'reminder-tomatoes',
    },
    {
      id: 'message-happy',
      sender: 'partner',
      text: '好耶！那我负责洗碗，成交 🫧',
      time: '18:33',
    },
  ],
  reminders: [
    {
      id: 'reminder-tomatoes',
      title: '一起买番茄 🍅',
      time: '19:00',
      assignee: 'both',
      completed: false,
    },
  ],
}

/** Return fresh nested objects so UI edits never mutate the seed fixture. */
export function createInitialState(): RelationshipState {
  return JSON.parse(JSON.stringify(initialRelationshipState)) as RelationshipState
}
