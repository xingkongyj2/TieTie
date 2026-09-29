import { useCallback, useEffect, useState } from 'react';
import { relationshipApi } from '../api/relationship';
import type { AISettings, Member, RelationshipState, Reminder } from '../types';

/** 本地资料和提醒状态；云端聊天由 useCloudChat 独立维护。 */
export function useRelationship() {
  const [state, setState] = useState<RelationshipState | null>(null);
  const [error, setError] = useState('');

  const reload = useCallback(async () => {
    try {
      setState(await relationshipApi.getState());
      setError('');
    } catch {
      setError('小窝暂时没加载出来，再试一次吧。');
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  const saveMember = async (member: Member) => {
    await relationshipApi.saveMember(member);
    await reload();
  };
  const saveSettings = async (settings: AISettings) => {
    await relationshipApi.saveSettings(settings);
    await reload();
  };
  const addReminder = async (input: Omit<Reminder, 'id' | 'completed'>) => {
    await relationshipApi.addReminder(input);
    await reload();
  };
  const toggleReminder = async (id: string) => {
    await relationshipApi.toggleReminder(id);
    await reload();
  };

  return { state, error, reload, saveMember, saveSettings, addReminder, toggleReminder };
}
