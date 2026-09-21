package service

import (
	"net/url"
	"testing"
)

// 动态代理 iplist URL 的掩码契约:状态接口不得泄露敏感 query 参数,
// 且前端把掩码回显原样交回时不得用占位符覆盖库里的真实凭据。
func TestMaskIplistURLRoundtrip(t *testing.T) {
	stored := "http://iplist.example.com/api?custom=ABC123&password=sekret&num=1"
	masked := MaskIplistURL(stored)
	if masked == "" || masked == stored {
		t.Fatalf("mask failed: %q", masked)
	}
	if !HasMaskedIplistQuery(masked) {
		t.Fatalf("masked url not detected: %q", masked)
	}
	q := queryOf(t, masked)
	if q["custom"] != "***" || q["password"] != "***" {
		t.Fatalf("secrets leaked into masked url: %q", masked)
	}
	if q["num"] != "1" {
		t.Fatalf("non-secret param lost: %q", masked)
	}

	// 前端把掩码回显原样交回(另改 num)→ 敏感参数从库值还原,其余以传入为准
	merged := mergeMaskedIplistURL(masked+"&extra=1&num=2", stored)
	mq := queryOf(t, merged)
	if mq["custom"] != "ABC123" || mq["password"] != "sekret" {
		t.Fatalf("secrets not restored from stored value: %q", merged)
	}
	if mq["extra"] != "1" || mq["num"] != "2" {
		t.Fatalf("non-secret params not taken from incoming: %q", merged)
	}

	// 全新真实 URL(无掩码占位)不得被改写
	fresh := "http://other.example.com/api?custom=NEW&num=2"
	if out := mergeMaskedIplistURL(fresh, stored); out != fresh {
		t.Fatalf("fresh url rewritten: %q != %q", out, fresh)
	}
}

func queryOf(t *testing.T, raw string) map[string]string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	out := map[string]string{}
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
