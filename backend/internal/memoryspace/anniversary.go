package memoryspace

import "time"

func ValidAnniversaryDate(value string) bool {
	date, err := time.Parse("2006-01-02", value)
	return err == nil && date.Format("2006-01-02") == value
}

func AnniversaryKindLabel(kind string) (string, bool) {
	labels := map[string]string{"together": "在一起", "birthday": "生日", "wedding": "结婚", "first_meet": "初次相遇", "other": "纪念日"}
	label, ok := labels[kind]
	return label, ok
}
