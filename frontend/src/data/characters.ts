/**
 * 角色素材目录集中在这里。新增 IP 时只要加入一个条目并放入对应 PNG，
 * 聊天头像和资料选择器会自动使用同一份资源。
 */
export interface Character {
  id: string;
  name: string;
  animal: string;
  personality: string;
  avatar: string;
  color: string;
}

export const characters: Character[] = [
  { id: 'golden-longhair-cat', name: '云朵喵', animal: '浅金长毛猫', personality: '抬爪问好', avatar: '/avatars/golden-longhair-cat.png', color: '#f8eddd' },
  // 本轮选中的五位放在图鉴前排，打开即可看到；新版兔子替代原来的紫色兔子。
  { id: 'zodiac-rat', name: '栗栗鼠', animal: '小老鼠', personality: '机灵探头', avatar: '/avatars/zodiac-rat.png', color: '#eee7ef' },
  { id: 'zodiac-ox', name: '牛牛宝', animal: '小牛', personality: '稳稳坐好', avatar: '/avatars/zodiac-ox.png', color: '#efe9de' },
  { id: 'zodiac-rabbit', name: '月芽兔', animal: '小兔子', personality: '安静听你说', avatar: '/avatars/zodiac-rabbit.png', color: '#f6efe4' },
  { id: 'zodiac-dragon', name: '小青龙', animal: '小龙', personality: '盘尾守护', avatar: '/avatars/zodiac-dragon.png', color: '#e1f4ec' },
  { id: 'zodiac-goat', name: '绵绵羊', animal: '小羊', personality: '软软陪伴', avatar: '/avatars/zodiac-goat.png', color: '#f2ece8' },
  { id: 'blue-cat', name: '贴贴喵', animal: '猫咪', personality: '机灵助攻', avatar: '/avatars/ai-cat.png', color: '#e7edff' },
  { id: 'cream-cat', name: '奶油喵', animal: '猫咪', personality: '温柔慢热', avatar: '/avatars/cream-cat.png', color: '#f4f0e9' },
  { id: 'peach-cat', name: '桃桃喵', animal: '猫咪', personality: '甜甜调皮', avatar: '/avatars/peach-cat.png', color: '#f9e8e8' },
  { id: 'corgi-dog', name: '柯基汪', animal: '小狗', personality: '热情挥手', avatar: '/avatars/corgi-dog.png', color: '#fff0dc' },
  { id: 'sloth', name: '慢慢懒', animal: '树懒', personality: '软软拥抱', avatar: '/avatars/sloth.png', color: '#f0e7f2' },
  { id: 'otter', name: '泡泡獭', animal: '水獭', personality: '害羞心动', avatar: '/avatars/otter.png', color: '#def5f1' },
  { id: 'penguin', name: '团团鹅', animal: '企鹅', personality: '认真招手', avatar: '/avatars/penguin.png', color: '#e8ebf3' },
];

export function characterForAvatar(avatar: string) {
  return characters.find((character) => character.avatar === avatar);
}
