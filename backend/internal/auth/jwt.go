// Package auth 提供登录鉴权能力：JWT 签发/解析与密码哈希。
// 纯逻辑包，不依赖 HTTP；Bearer 校验中间件在 api 层（复用统一错误响应）。
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Service 负责 JWT 的签发与解析。
type Service struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

// NewService 用密钥与有效期构造鉴权服务。
func NewService(secret string, ttl time.Duration) *Service {
	return &Service{secret: []byte(secret), ttl: ttl, issuer: "tietie-backend"}
}

// Claims 是令牌载荷：用户 ID 与用户名。
type Claims struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// IssueToken 为登录成功的用户签发令牌。
func (s *Service) IssueToken(userID, username string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// ErrInvalidToken 表示令牌签名/格式无效。
var ErrInvalidToken = errors.New("invalid token")

// ParseToken 校验并解析令牌；过期错误可通过 errors.Is(err, jwt.ErrTokenExpired) 判断。
func (s *Service) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, ErrInvalidToken
			}
			return s.secret, nil
		},
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, err
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ---- 请求上下文中的当前用户 ----

type ctxKey struct{}

// ContextWithClaims 把解析出的 Claims 挂到请求上下文（api 中间件调用）。
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, claims)
}

// ClaimsFrom 从上下文取当前登录用户；未登录返回 nil。
func ClaimsFrom(ctx context.Context) *Claims {
	claims, _ := ctx.Value(ctxKey{}).(*Claims)
	return claims
}

// UserIDFrom 从上下文取当前登录用户 ID；未登录返回空串。
func UserIDFrom(ctx context.Context) string {
	if claims := ClaimsFrom(ctx); claims != nil {
		return claims.UserID
	}
	return ""
}
