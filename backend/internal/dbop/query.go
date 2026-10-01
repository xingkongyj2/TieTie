package dbop

import "context"

// GetBindingBySessionID 返回当前仍生效的共享空间绑定。
func (db *DB) GetBindingBySessionID(ctx context.Context, sessionID string) (*Binding, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	return bindingForSession(db.gdb.WithContext(ctx), sessionID)
}

// ListBindings 返回全部当前绑定，供后台会话任务遍历。
func (db *DB) ListBindings(ctx context.Context) ([]Binding, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	bindings := make([]Binding, 0)
	err := db.gdb.WithContext(ctx).Order("created_at ASC").Find(&bindings).Error
	return bindings, err
}
