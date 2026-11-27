package main

// secrets.go：把访问令牌加密后再写进 config.json。
// Windows 用 DPAPI（绑定当前用户，换机器或换用户都解不开）；其它平台没有系统密钥库，
// 退回 0600 配置文件里的明文，界面会说明。老配置里的明文值照常能读。

import (
	"encoding/base64"
	"strings"
)

// secretDPAPIPrefix 标记 DPAPI 加密过的值，避免重新加密或把密文当明文用。
const secretDPAPIPrefix = "dpapi:v1:"

// sealSecret 把明文令牌封成可落盘的字符串；空串原样返回。
func sealSecret(plain string) string {
	plain = strings.TrimSpace(plain)
	if plain == "" || strings.HasPrefix(plain, secretDPAPIPrefix) {
		return plain
	}
	sealed, err := protectSecret([]byte(plain))
	if err != nil || len(sealed) == 0 {
		// 拿不到系统密钥库时只能用明文，配置文件本身是 0600。
		return plain
	}
	return secretDPAPIPrefix + base64.StdEncoding.EncodeToString(sealed)
}

// openSecret 解出明文；不是密文就按明文处理（兼容老配置）。
func openSecret(stored string) string {
	stored = strings.TrimSpace(stored)
	if !strings.HasPrefix(stored, secretDPAPIPrefix) {
		return stored
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretDPAPIPrefix))
	if err != nil {
		return ""
	}
	plain, err := unprotectSecret(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}

// secretProtected 说明这台机器上的令牌是否会加密落盘，界面按它提示。
func secretProtected() bool {
	probe, err := protectSecret([]byte("agentdeckd"))
	return err == nil && len(probe) > 0
}
