package ldapauth

import "testing"

func TestConfigurationRequiresEncryptedTransportAndSafeTemplate(t *testing.T) {
	valid := Config{URL: "ldap://dc.example.test:389", StartTLS: true, BindDN: "cn=svc,dc=example,dc=test", BaseDN: "dc=example,dc=test", UserFilter: "(sAMAccountName={username})"}
	if _, err := New(valid); err != nil {
		t.Fatalf("valid LDAP configuration rejected: %v", err)
	}
	plain := valid
	plain.StartTLS = false
	if _, err := New(plain); err == nil {
		t.Fatal("unencrypted LDAP configuration accepted")
	}
	unsafe := valid
	unsafe.UserFilter = "(sAMAccountName=admin)"
	if _, err := New(unsafe); err == nil {
		t.Fatal("LDAP filter without escaped username placeholder accepted")
	}
}
