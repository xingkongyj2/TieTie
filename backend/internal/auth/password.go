package auth

import "golang.org/x/crypto/bcrypt"

// VerifyPassword 仅用于旧账号首次登录时校验其不可逆的历史哈希。
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
