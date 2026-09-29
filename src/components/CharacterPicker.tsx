import { Check, Palette, Sparkles } from 'lucide-react';
import { characters } from '../data/characters';
import { Sheet } from './Sheet';

interface Props {
  selectedAvatar: string;
  ownerName: string;
  onSelect: (avatar: string) => void;
  onClose: () => void;
}

/** 只选择本地素材；资料表单统一负责保存，避免覆盖尚未提交的名字和偏好。 */
export function CharacterPicker({ selectedAvatar, ownerName, onSelect, onClose }: Props) {
  return <Sheet title="来挑一位小伙伴" subtitle={`给${ownerName}换个新形象，喜欢哪一只？`} onClose={onClose}>
    <div className="character-picker-intro"><Palette size={15} /><span>{characters.length} 位伙伴，{characters.length} 种小性格</span><Sparkles size={14} /></div>
    <div className="character-grid">{characters.map((character) => {
      const selected = character.avatar === selectedAvatar;
      const featured = character.id === 'golden-longhair-cat';
      return <button type="button" key={character.id} className={`character-option ${selected ? 'selected' : ''} ${featured ? 'character-featured' : ''}`} aria-label={`选择${character.name}，${character.animal}，${character.personality}`} aria-pressed={selected} onClick={() => { onSelect(character.avatar); onClose(); }}>
        <span className="character-image" style={{ backgroundColor: character.color }}><img src={character.avatar} alt="" loading="lazy" />{selected && <span className="character-selected"><Check size={12} /></span>}</span>
        <span className="character-info">{featured && <span className="character-new">PHOTO INSPIRED · NEW</span>}<strong>{character.name}</strong><small>{character.animal} · {character.personality}</small>{featured && <span className="character-featured-note">奶金色的小云朵，今天也来打招呼 ♡</span>}</span>
      </button>;
    })}</div>
    <p className="character-picker-note">选好后，记得在资料页点「保存」哦 ✧</p>
  </Sheet>;
}
