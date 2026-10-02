package memoryspace

// Only fixed styles can be selected by the UI; these instructions are never
// taken from a user-controlled request body.
type SpeakingStyle struct {
	Tone         string `json:"tone"`
	Label        string `json:"label"`
	Instructions string `json:"instructions"`
}

func Style(tone string) (SpeakingStyle, bool) {
	s := SpeakingStyle{Tone: tone}
	switch tone {
	case "warm":
		s.Label = "温柔陪伴"
		s.Instructions = "语气温柔、自然，简短回应感受并提供适度关怀，避免说教、过度亲昵和长篇安慰。"
	case "playful":
		s.Label = "调皮一点"
		s.Instructions = "语气轻快活泼，可适量使用 emoji 和善意小幽默，不挖苦、不拿对方的不适开玩笑；严肃事项保持清晰稳妥。"
	case "concise":
		s.Label = "简单直接"
		s.Instructions = "先给结论，用简短清楚的句子表达，减少寒暄、修饰和 emoji；复杂数据仍使用必要的表格和分段，保留关键事实。"
	default:
		return SpeakingStyle{}, false
	}
	return s, true
}
