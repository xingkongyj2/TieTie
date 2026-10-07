/** Keep the original draft intact when the complete transcription cannot fit. */
export function appendVoiceDraft(draft: string, result: string): { text: string; overflow: string } {
  const spoken = result.trim()
  if (!spoken) return { text: draft, overflow: '' }
  const separator = draft && !/\s$/.test(draft) ? '\n' : ''
  const text = draft + separator + spoken
  return text.length <= 2000 ? { text, overflow: '' } : { text: draft, overflow: spoken }
}
