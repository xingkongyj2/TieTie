import { Check } from 'lucide-react';
import { characters } from '../data/characters';
import { Sheet } from './Sheet';

interface Props {
  selectedAvatar: string;
  onSelect: (avatar: string) => void;
  onClose: () => void;
}

/** 只选择本地素材；资料表单统一负责保存，避免覆盖尚未提交的名字和偏好。 */
export function CharacterPicker({ selectedAvatar, onSelect, onClose }: Props) {
  return <Sheet title="选择小形象" onClose={onClose}>
    <div className="character-grid">{characters.map((character) => {
      const selected = character.avatar === selectedAvatar;
      return <button type="button" key={character.id} className={`character-option ${selected ? 'selected' : ''}`} aria-label={`选择${character.name}，${character.animal}，${character.personality}`} aria-pressed={selected} onClick={() => { onSelect(character.avatar); onClose(); }}>
        <span className="character-image" style={{ backgroundColor: character.color }}><img src={character.avatar} alt="" loading="lazy" />{selected && <span className="character-selected"><Check size={12} /></span>}</span>
        <span className="character-info"><strong>{character.name}</strong><small>{character.animal}</small></span>
      </button>;
    })}</div>
  </Sheet>;
}
