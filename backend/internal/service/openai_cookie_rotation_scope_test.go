package service

import "testing"

func TestIsOpenAICookieRotationAccountRequiresConfiguredScope(t *testing.T) {
	settings := &OpenAICookieSettings{RotationGroupIDs: []int64{1}}
	inScope := &Account{ID: 1, GroupIDs: []int64{1}}
	outOfScope := &Account{ID: 2, GroupIDs: []int64{2}}
	if !isOpenAICookieRotationAccount(inScope, settings) {
		t.Fatal("account in configured rotation group should be eligible")
	}
	if isOpenAICookieRotationAccount(outOfScope, settings) {
		t.Fatal("account outside configured rotation group must not be eligible")
	}
}

func TestIsOpenAICookieRotationAccountEmptyScopeDisablesRotation(t *testing.T) {
	if isOpenAICookieRotationAccount(&Account{ID: 2, GroupIDs: []int64{2}}, &OpenAICookieSettings{}) {
		t.Fatal("empty rotation scope must not opt every account into rotation")
	}
}

func TestCookieCollectorOutsideRotationScopeCannotRotate(t *testing.T) {
	settings := &OpenAICookieSettings{
		CookieRotationEnabled: true,
		AutoValidateHost:      false,
		RotationGroupIDs:      []int64{1},
	}
	account := &Account{ID: 2, GroupIDs: []int64{2}}
	if isOpenAICookieRotationAccount(account, settings) {
		t.Fatal("test account must be outside rotation scope")
	}
	derived := openAICookieProbeSettings(account, settings)
	if derived == nil {
		t.Fatal("derived settings must not be nil")
	}
	if derived.CookieRotationEnabled {
		t.Fatal("out-of-scope collector must not retain cookie rotation")
	}
	if derived.AutoValidateHost {
		t.Fatal("out-of-scope collector must not auto-bind a Host")
	}
	if derived.autoConfigureOtherAccounts {
		t.Fatal("out-of-scope collector must not fan out Host bindings")
	}
}

func TestCookieCollectorInRotationScopeRetainsRotation(t *testing.T) {
	settings := &OpenAICookieSettings{CookieRotationEnabled: true, RotationGroupIDs: []int64{1}}
	derived := openAICookieProbeSettings(&Account{ID: 1, GroupIDs: []int64{1}}, settings)
	if derived != settings {
		t.Fatal("in-scope account should use the configured settings")
	}
}
