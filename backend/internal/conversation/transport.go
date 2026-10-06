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

// Replacing the historical instructions field removes obsolete server prompt
// copies while preserving the current speaking style and memory additions.
const CloudInstructionsNotice = "固定身份、行为和 tietie 协议由云端内置系统提示词统一定义。此文件只保留当前说话方式和追加记忆；旧服务端固定提示词不再生效。"

// A transport revision resets dynamic snapshots without hashing cloud persona.
const transportContractVersion = "tietie.conversation/2/cloud-system-prompt/1"

func ContractHash(visibility string) string {
	sum := sha256.Sum256([]byte(transportContractVersion + "\x00" + visibility))
	return hex.EncodeToString(sum[:])
}

type TransportState struct {
	WeatherProfiles  []WeatherProfile           `json:"weatherProfiles,omitempty"`
	CountdownBoard   *CountdownBoard            `json:"countdownBoard,omitempty"`
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
	next.WeatherProfiles = e.WeatherProfiles
	next.CountdownBoard = e.CountdownBoard
	next.AssistantStyle = e.AssistantStyle
	next.AnniversaryBoard = e.AnniversaryBoard
	// Template paths are already part of the cloud system prompt.
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
	if previous == nil {
		e.Timezone = "Asia/Shanghai"
		e.Reminders = next.Reminders
		e.MemoryIndex = next.MemoryIndex
	} else {
		if reflect.DeepEqual(next.WeatherProfiles, previous.WeatherProfiles) {
			e.WeatherProfiles = nil
		}
		if reflect.DeepEqual(next.CountdownBoard, previous.CountdownBoard) {
			e.CountdownBoard = nil
		}
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
	return openV2 + string(body) + closeV2, next
}
