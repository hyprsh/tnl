package dns

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libdns/libdns"
)

// echo is a provider that puts the token it was given into every error, as a
// provider's API error may.
type echo struct {
	Token string `json:"api_token"`
	deny  bool
}

func (e *echo) GetRecords(context.Context, string) ([]libdns.Record, error) {
	if e.deny {
		return nil, errors.New("403 for Authorization: Bearer " + e.Token)
	}
	return nil, nil
}
func (e *echo) AppendRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	return nil, errors.New("append failed with " + e.Token)
}
func (e *echo) DeleteRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	return nil, errors.New("delete failed with " + e.Token)
}

const secret = "s3cr3t-T0KEN-value"

func TestACredentialFieldReachesTheProviderAndNoErrorCarriesTheToken(t *testing.T) {
	Register("echo-test", func() Client { return new(echo) }, "api_token")
	c, err := Open("echo-test", secret)
	if err != nil {
		t.Fatal(err)
	}
	_, e1 := c.AppendRecords(context.Background(), "example.test.", nil)
	_, e2 := c.DeleteRecords(context.Background(), "example.test.", nil)
	for _, e := range []error{e1, e2} {
		if e == nil || strings.Contains(e.Error(), secret) || !strings.Contains(e.Error(), "[redacted]") {
			t.Errorf("error %v", e)
		}
	}
	// The provider got the token: its errors only carry it if it did.
	if !strings.Contains(e1.Error(), "append failed with [redacted]") {
		t.Errorf("the provider did not get the token: %v", e1)
	}
}

func TestCheckRefusesATokenThatCannotReadTheZoneWithoutTheToken(t *testing.T) {
	Register("echo-deny", func() Client { return &echo{deny: true} }, "api_token")
	err := Check(context.Background(), "echo-deny", secret, "example.test")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("%v", err)
	}
}

func TestAnUnknownProviderIsRefused(t *testing.T) {
	if _, err := Open("nope", "x"); err == nil || !strings.Contains(err.Error(), "not compiled in") {
		t.Fatalf("%v", err)
	}
}

func TestCloudflareIsRegistered(t *testing.T) {
	if _, ok := Lookup("cloudflare"); !ok {
		t.Fatal("cloudflare is not registered")
	}
}
