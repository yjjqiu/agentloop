package main

import "testing"

func TestSealSecretRoundTrip(t *testing.T) {
	const plain = "ghp_example_token_value"
	stored := sealSecret(plain)
	if stored == "" {
		t.Fatal("sealSecret 返回空")
	}
	if openSecret(stored) != plain {
		t.Fatalf("openSecret(sealSecret(x)) = %q, 期望 %q", openSecret(stored), plain)
	}
	if secretProtected() && stored == plain {
		t.Error("有系统密钥库时应当加密落盘")
	}
	if !secretProtected() && stored != plain {
		t.Error("没有系统密钥库时应当按明文保存")
	}
}

func TestSealSecretKeepsSealedValue(t *testing.T) {
	if !secretProtected() {
		t.Skip("当前平台没有系统密钥库")
	}
	once := sealSecret("token")
	if twice := sealSecret(once); twice != once {
		t.Errorf("已经加密过的值被二次加密: %q -> %q", once, twice)
	}
}

func TestSealSecretEmpty(t *testing.T) {
	if got := sealSecret("   "); got != "" {
		t.Errorf("空令牌应保存为空，得到 %q", got)
	}
	if got := openSecret(""); got != "" {
		t.Errorf("空值应解出空串，得到 %q", got)
	}
}

func TestOpenSecretLegacyPlaintext(t *testing.T) {
	if got := openSecret("legacy-plain-token"); got != "legacy-plain-token" {
		t.Errorf("老配置的明文令牌应原样返回，得到 %q", got)
	}
	if got := openSecret(secretDPAPIPrefix + "not-base64!"); got != "" {
		t.Errorf("损坏的密文应返回空串，得到 %q", got)
	}
}
