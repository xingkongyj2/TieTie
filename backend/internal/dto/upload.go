// Package dto 定义前端请求的校验规则与大小限制。
// 白名单内容与 frontend/src/api/upload-types.json 保持同步（该文件是契约源，改动时两边一起改）。
package dto

import "strings"

// 请求体与附件大小限制（对应 qoder.mjs 顶部常量）。
const (
	MaxBodyBytes      = 12 * 1024 * 1024
	MaxFileBytes      = 4 * 1024 * 1024
	MaxImageBase64    = 10 * 1024 * 1024
	MaxDocumentBase64 = (MaxFileBytes/3 + 1) * 4 // ceil(4MB/3)*4
	MaxTextRunes      = 2000
	MaxAttachments    = 4
	MaxNameRunes      = 255
)

// ImageTypes 是允许内联发送的图片 MIME。
var ImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// 文本类附件的 application/* MIME 白名单。
var textApplicationMimes = map[string]bool{
	"application/json": true, "application/xml": true, "application/javascript": true,
	"application/x-yaml": true, "application/x-toml": true,
}

var textExtensions = []string{
	".txt", ".md", ".csv", ".json", ".xml", ".yaml", ".yml", ".toml",
	".ini", ".conf", ".cfg", ".env", ".log", ".html", ".htm", ".css",
	".scss", ".less", ".js", ".jsx", ".ts", ".tsx", ".vue", ".svelte",
	".py", ".go", ".rs", ".java", ".kt", ".scala", ".c", ".cpp",
	".cc", ".h", ".hpp", ".rb", ".php", ".swift", ".r", ".lua",
	".pl", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".sql",
	".graphql", ".gql", ".proto", ".dockerfile", ".makefile",
	".gitignore", ".editorconfig", ".eslintrc", ".prettierrc", ".tex",
	".rst", ".adoc", ".org", ".svg",
}

var extensionlessNames = map[string]bool{
	"dockerfile": true, "makefile": true, "gemfile": true, "rakefile": true,
	"procfile": true, "vagrantfile": true, "justfile": true, "brewfile": true,
}

// SupportedTextName 判断文件名是否属于文本类白名单。
func SupportedTextName(name string) bool {
	lower := strings.ToLower(name)
	if extensionlessNames[lower] {
		return true
	}
	for _, ext := range textExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// SupportedTextMime 判断 MIME 是否为文本类，并返回去掉参数的规范化形式。
func SupportedTextMime(mime string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if strings.HasPrefix(normalized, "text/") || textApplicationMimes[normalized] {
		return normalized, true
	}
	return normalized, false
}

// InvalidFileName 检查附件名是否包含路径分隔符或控制字符。
func InvalidFileName(name string) bool {
	return strings.ContainsAny(name, "/\\\x00\r\n")
}
