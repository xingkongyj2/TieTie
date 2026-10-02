import { useCallback, useEffect, useRef, useState } from 'react';
import { relationshipApi } from '../api/relationship';
import { profileApi } from '../api/profile';
import { assistantSettingsApi } from '../api/assistant-settings';
import type { AISettings, Member, RelationshipState, Reminder } from '../types';

/** Account profiles and the conversation's speaking style come from the server. */
export function useRelationship(userId?: number, partnerId?: number, sessionId?: string) {
  const key = `${userId ?? ''}:${partnerId ?? ''}:${sessionId ?? ''}`;
  const scope = useRef(key);
  scope.current = key;
  const sequence = useRef(0);
  const [loaded, setLoaded] = useState<{ key: string; state: RelationshipState } | null>(null);
  const [error, setError] = useState('');

  const reload = useCallback(async () => {
    const requestId = ++sequence.current;
    try {
      const [local, { profiles }, { settings: assistant }] = await Promise.all([
        relationshipApi.getState(),
        userId ? profileApi.get() : Promise.resolve({ profiles: [] }),
        sessionId ? assistantSettingsApi.get(sessionId) : Promise.resolve({ settings: { tone: 'warm' as const } }),
      ]);
      const members = local.members.map((member) => {
        if (member.id === 'ai') return member;
        const id = member.id === 'self' ? userId : partnerId;
        const profile = profiles.find((item) => item.userId === id);
        // Never upload demo/local profiles or another account's cached facts.
        return { ...member, userId: id, gender: profile?.gender ?? 'unspecified' as const,
          birthday: profile?.birthday ?? '', hobbies: profile?.hobbies ?? [], bio: profile?.bio ?? '',
          avatar: profile?.avatar || (member.id === 'self' ? '/avatars/cream-cat.png' : '/avatars/peach-cat.png') };
      });
      if (scope.current !== key || requestId !== sequence.current) return;
      setLoaded({ key, state: { ...local, members, settings: { ...local.settings, tone: assistant.tone } } });
      setError('');
    } catch (error) {
      if (scope.current === key && requestId === sequence.current) setError(error instanceof Error ? error.message : '小档案暂时没加载出来，再试一次吧。');
    }
  }, [key, userId, partnerId, sessionId]);

  useEffect(() => { setError(''); void reload(); }, [reload]);

  const saveMember = async (member: Member) => {
    if (member.id !== 'self' || !userId) throw new Error('只能编辑自己的小档案。');
    const { profile } = await profileApi.save(member);
    if (scope.current !== key) return;
    setLoaded((current) => current?.key === key ? { ...current, state: { ...current.state,
      members: current.state.members.map((item) => item.id === 'self' ? { ...item,
        gender: profile.gender, birthday: profile.birthday, hobbies: profile.hobbies,
        bio: profile.bio, avatar: profile.avatar || '/avatars/cream-cat.png' } : item),
    } } : current);
    await reload();
  };
  const saveSettings = async (settings: AISettings) => {
    if (sessionId) await assistantSettingsApi.save(sessionId, settings.tone);
    if (scope.current !== key) return;
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

  return { state: loaded?.key === key ? loaded.state : null, error, reload, saveMember, saveSettings, addReminder, toggleReminder };
}
