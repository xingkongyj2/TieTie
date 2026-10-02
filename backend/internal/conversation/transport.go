package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"tietie/backend/internal/memoryspace"
)

const ProtocolMemoryPath = memoryspace.BehaviorPath
const incrementalInstructions = `
增量传输：首次或协议升级时提供本完整协议，后续 compact=true 的 TIETIE_INPUT_V2 不再重复提示词。固定协议副本在 rules/assistant-behavior.json 的 instructions 字段，遗忘协议时读取此字段；读取不可用时不得猜测控制格式或声称已保存。
members 是成员列表的替换快照，只在变化时发送。reminders、memoryIndex 是新增或变更条目，按 id/memoryKey 合并；removedReminderIds、removedMemoryKeys 只移除会话索引中的条目，不表示云端记忆已删除。省略字段表示沿用已知上下文，不表示空列表。当前时区始终 Asia/Shanghai。每轮 actor、requestId、currentTime 都是本次的新值，不沿用旧发言者或旧时间。`

// This supplements transport rules only; Qoder retains its configured persona.
func TransportInstructions(visibility string) string {
	value := InstructionsV2 + incrementalInstructions
	if visibility == "private" {
		value += privateInstructionsV2
	}
	return value
}
func ContractHash(visibility string) string {
	sum := sha256.Sum256([]byte(TransportInstructions(visibility)))
	return hex.EncodeToString(sum[:])
}

type TransportState struct {
	AnniversaryBoard *AnniversaryBoard          `json:"anniversaryBoard,omitempty"`
	AssistantStyle   *memoryspace.SpeakingStyle `json:"assistantStyle,omitempty"`
	Members          []Member                   `json:"members,omitempty"`
	Reminders        []Reminder                 `json:"reminders,omitempty"`
	MemoryIndex      []MemoryIndex              `json:"memoryIndex,omitempty"`
}

// State is advanced only after Qoder accepts the frame. Sorting avoids spurious
// deltas caused by database ordering, not actual changes in business data.
func CompactFrame(e EnvelopeV2, previous *TransportState) (string, TransportState) {
	next := TransportState{Members: append([]Member(nil), e.Members...), Reminders: append([]Reminder(nil), e.Reminders...), MemoryIndex: append([]MemoryIndex(nil), e.MemoryIndex...)}
	next.AssistantStyle = e.AssistantStyle
	next.AnniversaryBoard = e.AnniversaryBoard
	// Template paths are already part of the fixed contract/resource instructions.
	// They are not human facts and need no per-turn index entries.
	next.MemoryIndex = nil
	for _, memory := range e.MemoryIndex {
		if memory.Kind != "template" {
			memory.Kind = ""
			next.MemoryIndex = append(next.MemoryIndex, memory)
		}
	}
	sort.Slice(next.Members, func(i, j int) bool { return next.Members[i].ID < next.Members[j].ID })
	sort.Slice(next.Reminders, func(i, j int) bool { return next.Reminders[i].ID < next.Reminders[j].ID })
	sort.Slice(next.MemoryIndex, func(i, j int) bool { return next.MemoryIndex[i].Key < next.MemoryIndex[j].Key })
	e.Compact = true
	e.Members = next.Members
	e.Reminders = nil
	e.MemoryIndex = nil
	prefix := ""
	if previous == nil {
		prefix = TransportInstructions(e.Visibility)
		e.Timezone = "Asia/Shanghai"
		e.Reminders = next.Reminders
		e.MemoryIndex = next.MemoryIndex
	} else {
		if reflect.DeepEqual(next.AnniversaryBoard, previous.AnniversaryBoard) {
			e.AnniversaryBoard = nil
		}
		if reflect.DeepEqual(next.AssistantStyle, previous.AssistantStyle) {
			e.AssistantStyle = nil
		}
		e.Timezone = ""
		if reflect.DeepEqual(next.Members, previous.Members) {
			e.Members = nil
		}
		beforeReminders := map[string]Reminder{}
		for _, r := range previous.Reminders {
			beforeReminders[r.ID] = r
		}
		for _, r := range next.Reminders {
			old, ok := beforeReminders[r.ID]
			if !ok || !reflect.DeepEqual(old, r) {
				e.Reminders = append(e.Reminders, r)
			}
			delete(beforeReminders, r.ID)
		}
		for id := range beforeReminders {
			e.RemovedReminderIDs = append(e.RemovedReminderIDs, id)
		}
		beforeMemory := map[string]MemoryIndex{}
		for _, m := range previous.MemoryIndex {
			beforeMemory[m.Key] = m
		}
		for _, m := range next.MemoryIndex {
			old, ok := beforeMemory[m.Key]
			if !ok || !reflect.DeepEqual(old, m) {
				e.MemoryIndex = append(e.MemoryIndex, m)
			}
			delete(beforeMemory, m.Key)
		}
		for key := range beforeMemory {
			e.RemovedMemoryKeys = append(e.RemovedMemoryKeys, key)
		}
		sort.Strings(e.RemovedReminderIDs)
		sort.Strings(e.RemovedMemoryKeys)
	}
	body, _ := json.Marshal(e)
	return prefix + openV2 + string(body) + closeV2, next
}
