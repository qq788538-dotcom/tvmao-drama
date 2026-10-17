package http

import (
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/localauth"
)

// 桌面端 CLI 鉴权是 localauth 的静态 CLI token，不是 JWT。
const testCLIToken = "cli-test-token-0123456789abcdef0123"

func cliTestAuth() localauth.Config {
	return localauth.Config{
		Token:    "web-test-token-0123456789abcdef0123",
		CLIToken: testCLIToken,
	}
}

func cliBearer() string { return "Bearer " + testCLIToken }
